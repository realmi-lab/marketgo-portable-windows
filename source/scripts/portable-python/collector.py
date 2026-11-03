"""Bounded REST and WebSocket workers with explicit state and recovery."""
from __future__ import annotations

import json
import logging
import ssl
import threading
import time
import urllib.request
import uuid
from decimal import Decimal

import certifi
import websocket

from adapters import CHUNK_SIZES, REGISTRIES, SOCKETS, TICKERS, VENUES, ensure_fresh, registry, subscriptions, tickers, trades
from engine import Engine
from hourly import HourlyCollector

LOG = logging.getLogger('marketgo')


def abort_socket(socket):
    """Unblock receive/send before closing, including during application exit."""
    if socket is None:
        return
    socket.keep_running = False
    core = socket.sock
    if core is not None:
        core.abort()
        core.shutdown()
    socket.close(timeout=0)


def tls_context() -> ssl.SSLContext:
    # Keep OS trust (including Windows enterprise roots) and add the bundled
    # public roots. Never disable certificate or hostname verification.
    context = ssl.create_default_context()
    context.load_verify_locations(certifi.where())
    context.minimum_version = ssl.TLSVersion.TLSv1_2
    return context


def get_json(url: str, context: ssl.SSLContext):
    request = urllib.request.Request(url, headers={'User-Agent': 'MarketGO-Portable-Python/3.0', 'Accept': 'application/json',
                                                 'Cache-Control': 'no-cache'})
    with urllib.request.urlopen(request, context=context, timeout=8) as response:
        if int(response.headers.get('Age', '0')) > 120:
            raise ValueError('Official HTTP response is cached for more than 120 seconds')
        raw = response.read(8 * 1024 * 1024 + 1)
    if len(raw) > 8 * 1024 * 1024:
        raise ValueError('Official response exceeds 8 MiB')
    return json.loads(raw, parse_float=Decimal)


class TradeWorker:
    def __init__(self, engine: Engine, venue: str, key: str, markets: dict, context: ssl.SSLContext):
        self.engine, self.venue, self.key = engine, venue, key
        self.markets = markets
        self.context = context
        self.done = threading.Event()
        self.socket: websocket.WebSocketApp | None = None
        self.last_ping = 0.0
        self.last_error = ''
        self.thread = threading.Thread(target=self.run, name=f'trade-{key}', daemon=True)

    def start(self):
        self.thread.start()

    def close(self):
        self.done.set()
        if self.socket:
            abort_socket(self.socket)

    def on_open(self, socket):
        self.engine.feed_status(self.key, self.venue, 'WAITING')
        self.last_error = ''
        self.last_ping = time.monotonic()
        try:
            for payload in subscriptions(self.venue, self.markets, uuid.uuid4().hex):
                if self.done.is_set() or self.engine.stop.is_set():
                    return
                socket.send(json.dumps(payload, separators=(',', ':')))
                if len(self.markets) > 1 and self.venue in ('coinone', 'gopax'):
                    self.done.wait(0.025)
            self.engine.subscription_ready(self.key)
        except Exception as exc:
            self.on_error(socket, exc)

    def on_message(self, socket, message):
        try:
            text = message.decode('utf-8') if isinstance(message, bytes) else message
            self.engine.heartbeat(self.key)
            if '::ping::' in text:
                socket.send(text.replace('::ping::', '::pong::'))
                return
            payload = json.loads(text, parse_float=Decimal)
            if not isinstance(payload, dict):
                return
            for trade in trades(self.venue, payload):
                self.engine.accept(self.key, self.venue, trade, set(self.markets))
        except Exception as exc:
            # Parsing a recognized trade must not silently certify coverage.
            LOG.error('%s parser: %s', self.key, exc)
            self.last_error = f'Parser: {exc}'
            self.engine.feed_status(self.key, self.venue, 'ERROR', self.last_error)
            abort_socket(socket)

    def on_error(self, socket, error):
        if not self.done.is_set() and not self.engine.stop.is_set():
            self.last_error = str(error)
            self.engine.feed_status(self.key, self.venue, 'ERROR', self.last_error)
            LOG.warning('%s websocket: %s', self.key, error)
            if socket:
                abort_socket(socket)

    def on_close(self, _socket, status, message):
        if not self.done.is_set() and not self.engine.stop.is_set():
            self.engine.feed_status(self.key, self.venue, 'ERROR', self.last_error or f'WebSocket closed ({status}): {message or "reconnecting"}')

    def heartbeat(self):
        socket = self.socket
        with self.engine.lock:
            feed = self.engine.feeds.get(self.key)
        if socket and socket.sock and socket.sock.connected and feed and self.engine.clock() - feed.at > 90:
            self.on_error(socket, TimeoutError('No websocket or heartbeat message for 90 seconds'))
            return
        if self.venue == 'coinone' and socket and socket.sock and socket.sock.connected and time.monotonic() - self.last_ping >= 20:
            try:
                socket.send('{"request_type":"PING"}')
                self.last_ping = time.monotonic()
            except Exception as exc:
                self.on_error(socket, exc)
                abort_socket(socket)

    def run(self):
        delay = 1
        while not self.done.is_set() and not self.engine.stop.is_set():
            self.engine.feed_status(self.key, self.venue, 'CONNECTING')
            started = time.monotonic()
            try:
                self.socket = websocket.WebSocketApp(SOCKETS[self.venue], on_open=self.on_open,
                    on_message=self.on_message, on_error=self.on_error, on_close=self.on_close,
                    on_pong=lambda _ws, _data: self.engine.heartbeat(self.key))
                protocol_ping = self.venue not in ('gopax', 'coinone')
                self.socket.run_forever(sslopt={'context': self.context, 'cert_reqs': ssl.CERT_REQUIRED, 'check_hostname': True},
                                       ping_interval=20 if protocol_ping else 0,
                                       ping_timeout=10 if protocol_ping else None, suppress_origin=True)
            except Exception as exc:
                self.on_error(self.socket, exc)
            finally:
                if self.socket:
                    abort_socket(self.socket)
            if time.monotonic() - started > 60:
                delay = 1
            if self.done.wait(delay):
                return
            delay = min(30, delay * 2)


class Collector:
    def __init__(self, engine: Engine, context=None):
        self.engine = engine
        self.context = context if context is not None else tls_context()
        websocket.setdefaulttimeout(8)
        self.threads = []
        self.workers: dict[str, TradeWorker] = {}
        self.fingerprints = {}
        self.lock = threading.Lock()

    def ticker_loop(self, venue: str):
        markets, refreshed = {}, 0.0
        while not self.engine.stop.is_set():
            started = time.monotonic()
            try:
                if not markets or time.monotonic() - refreshed > 300:
                    markets = registry(venue, get_json(REGISTRIES[venue], self.context))
                    refreshed = time.monotonic()
                payload = get_json(TICKERS[venue], self.context)
                amounts = tickers(venue, payload, markets)
                ensure_fresh(venue, payload, markets, self.engine.clock())
                self.engine.update_ticker(venue, markets, amounts)
            except Exception as exc:
                self.engine.update_ticker(venue, {}, {}, error=str(exc))
                LOG.warning('%s ticker: %s', venue, exc)
            self.engine.stop.wait(max(1, 10 - (time.monotonic() - started)))

    def supervise(self):
        while not self.engine.stop.wait(0.5):
            for venue in VENUES:
                with self.engine.lock:
                    snapshot = self.engine.tickers[venue]
                    markets = dict(snapshot.markets)
                fingerprint = tuple(sorted(markets.items()))
                if markets and self.fingerprints.get(venue) != fingerprint:
                    with self.lock:
                        for key in list(self.workers):
                            if self.workers[key].venue == venue:
                                self.workers.pop(key).close()
                        pairs = list(sorted(markets.items()))
                        chunks = {f'{venue}-{offset}': dict(pairs[offset:offset + CHUNK_SIZES[venue]])
                                  for offset in range(0, len(pairs), CHUNK_SIZES[venue])}
                        self.engine.set_expected(venue, set(chunks))
                        for key, chunk in chunks.items():
                            worker = TradeWorker(self.engine, venue, key, chunk, self.context)
                            self.workers[key] = worker
                            worker.start()
                        self.fingerprints[venue] = fingerprint
                    LOG.info('%s: official ticker for %s KRW markets, %s websocket workers', venue, len(markets), len(chunks))
            with self.lock:
                workers = list(self.workers.values())
            for worker in workers:
                worker.heartbeat()

    def start(self):
        for venue in VENUES:
            thread = threading.Thread(target=self.ticker_loop, args=(venue,), name=f'ticker-{venue}', daemon=True)
            self.threads.append(thread)
            thread.start()
        supervisor = threading.Thread(target=self.supervise, name='trade-supervisor', daemon=True)
        self.threads.append(supervisor)
        supervisor.start()
        if self.engine.archive is not None:
            hourly = HourlyCollector(self.engine, lambda url: get_json(url, self.context))
            for venue in VENUES:
                thread = threading.Thread(target=hourly.run, args=(venue,), name=f'hourly-{venue}', daemon=True)
                self.threads.append(thread)
                thread.start()

    def close(self):
        self.engine.stop.set()
        with self.lock:
            workers = list(self.workers.values())
        for worker in workers:
            worker.close()
        deadline = time.monotonic() + 3
        for thread in [*self.threads, *(w.thread for w in workers)]:
            thread.join(max(0, deadline - time.monotonic()))
