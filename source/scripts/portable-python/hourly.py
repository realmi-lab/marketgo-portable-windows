"""Current closed-hour collection and durable local storage, without backfill."""
from __future__ import annotations

from contextlib import closing, contextmanager
from datetime import datetime, timezone, timedelta
from decimal import Decimal
import logging
from pathlib import Path
import sqlite3
import threading
import time
from urllib.parse import urlencode, quote

from adapters import number

LOG = logging.getLogger('marketgo')
SUPPORTED = ('bithumb', 'upbit', 'coinone')
HOUR = 3600
PUBLICATION_DELAY = 120


def request_url(venue: str, base: str, start: int) -> str:
    if start <= 0 or start % HOUR or not base.isalnum():
        raise ValueError('Invalid current-hour request')
    end = start + HOUR
    if venue in ('bithumb', 'upbit'):
        zone = timezone(timedelta(hours=9)) if venue == 'bithumb' else timezone.utc
        to = datetime.fromtimestamp(end, zone).strftime('%Y-%m-%dT%H:%M:%S')
        if venue == 'upbit':
            to += 'Z'
        return f'https://api.{venue}.com/v1/candles/minutes/60?' + urlencode(
            {'market': f'KRW-{base}', 'count': 1, 'to': to})
    if venue == 'coinone':
        return f'https://api.coinone.co.kr/public/v2/chart/KRW/{quote(base)}?' + urlencode(
            {'interval': '1h', 'size': 1, 'timestamp': end * 1000 - 1})
    raise ValueError('Hourly quote turnover is unavailable for this venue')


def parse_candle(venue: str, base: str, start: int, payload) -> dict | None:
    if venue in ('bithumb', 'upbit'):
        if not isinstance(payload, list):
            raise ValueError('Invalid hourly candle response')
        rows = payload
    elif venue == 'coinone':
        if not isinstance(payload, dict) or payload.get('result') != 'success' or str(payload.get('error_code')) != '0':
            raise ValueError('Coinone hourly candle request failed')
        rows = payload.get('chart')
    else:
        raise ValueError('Unsupported hourly candle venue')
    if not isinstance(rows, list) or len(rows) > 1:
        raise ValueError('Expected at most one current-hour candle')
    result = None
    for item in rows:
        if venue in ('bithumb', 'upbit'):
            if item.get('market') != f'KRW-{base}' or item.get('unit') != 60:
                raise ValueError('Hourly candle market or unit mismatch')
            date = datetime.fromisoformat(item['candle_date_time_utc'].replace('Z', '+00:00'))
            if date.tzinfo is None:
                date = date.replace(tzinfo=timezone.utc)
            at = date.timestamp()
            opening, amount, volume = item.get('opening_price'), item.get('candle_acc_trade_price'), item.get('candle_acc_trade_volume')
        else:
            at = float(number(item.get('timestamp'))) / 1000
            opening, amount, volume = item.get('open'), item.get('quote_volume'), item.get('target_volume')
        if at % HOUR:
            raise ValueError('Hourly candle timestamp is not aligned')
        # Quiet-market APIs may return an older candle. It is not zero turnover
        # and must never be stored under the requested hour.
        if at != start:
            continue
        opening, amount, volume = number(opening), number(amount), number(volume)
        if opening <= 0:
            raise ValueError('Hourly opening price must be positive')
        result = dict(open_price=opening, amount=amount, base_volume=volume)
    return result


class HourlyArchive:
    def __init__(self, path: Path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.lock = threading.RLock()
        with self.connect() as db:
            db.execute('PRAGMA journal_mode=WAL')
            db.executescript('''
                CREATE TABLE IF NOT EXISTS hourly_windows (
                    venue TEXT NOT NULL, hour INTEGER NOT NULL, status TEXT NOT NULL,
                    PRIMARY KEY (venue, hour));
                CREATE TABLE IF NOT EXISTS hourly_candles (
                    venue TEXT NOT NULL, base TEXT NOT NULL, market TEXT NOT NULL,
                    hour INTEGER NOT NULL, status TEXT NOT NULL,
                    open_price TEXT, amount TEXT, base_volume TEXT, error TEXT,
                    PRIMARY KEY (venue, base, hour));
                CREATE INDEX IF NOT EXISTS hourly_candles_hour ON hourly_candles(hour);
            ''')
            # The launcher binds its local port before opening this archive.
            # Work interrupted by the previous process stays missing, not
            # permanently "collecting" and not eligible for automatic replay.
            db.execute("UPDATE hourly_candles SET status='MISSING' WHERE status='COLLECTING'")
            db.execute("UPDATE hourly_windows SET status='PARTIAL' WHERE status='COLLECTING'")
            self.markets = {(r['venue'], r['base']): r['market'] for r in db.execute(
                'SELECT DISTINCT venue, base, market FROM hourly_candles')}
            saved = db.execute("SELECT COUNT(*) AS n, MAX(hour) AS hour FROM hourly_candles WHERE status='COMPLETE'").fetchone()
            self.summary = dict(stored_candles=saved['n'], latest_hour=saved['hour'], active=False, error='')

    @contextmanager
    def connect(self):
        # A connection belongs to one operation, so stopping a daemon worker
        # cannot close another thread's SQLite handle.
        with self.lock, closing(sqlite3.connect(self.path, timeout=2)) as db:
            db.row_factory = sqlite3.Row
            with db:
                yield db

    def begin(self, venue: str, hour: int, markets: dict[str, str]) -> bool:
        if hour <= 0 or hour % HOUR or not markets:
            raise ValueError('Invalid hourly window')
        with self.connect() as db:
            inserted = db.execute('INSERT OR IGNORE INTO hourly_windows VALUES (?, ?, ?)',
                                  (venue, hour, 'COLLECTING')).rowcount
            if not inserted:
                return False
            status = 'COLLECTING' if venue in SUPPORTED else 'TURNOVER_UNAVAILABLE'
            db.executemany('INSERT INTO hourly_candles (venue,base,market,hour,status) VALUES (?,?,?,?,?)',
                           [(venue, base, market, hour, status) for base, market in sorted(markets.items())])
            self.markets.update({(venue, base): market for base, market in markets.items()})
            self.summary['active'] = True
            return True

    def save(self, venue: str, base: str, hour: int, candle: dict | None, error: str = ''):
        status = 'FAILED' if error else 'MISSING' if candle is None else 'COMPLETE'
        values = [str(candle[k]) if candle is not None else None for k in ('open_price', 'amount', 'base_volume')]
        with self.connect() as db:
            changed = db.execute('''UPDATE hourly_candles SET status=?,open_price=?,amount=?,base_volume=?,error=?
                WHERE venue=? AND base=? AND hour=? AND status='COLLECTING' ''',
                (status, *values, error[:300], venue, base, hour)).rowcount
            if changed and candle is not None:
                self.summary['stored_candles'] += 1
                self.summary['latest_hour'] = max(hour, self.summary['latest_hour'] or hour)

    def finish(self, venue: str, hour: int):
        with self.connect() as db:
            db.execute("UPDATE hourly_candles SET status='MISSING' WHERE venue=? AND hour=? AND status='COLLECTING'", (venue, hour))
            missing = db.execute("SELECT 1 FROM hourly_candles WHERE venue=? AND hour=? AND status!='COMPLETE' LIMIT 1", (venue, hour)).fetchone()
            db.execute('UPDATE hourly_windows SET status=? WHERE venue=? AND hour=?',
                       ('PARTIAL' if missing else 'COMPLETE', venue, hour))

    def snapshot(self, start: int, end: int) -> list[dict]:
        with self.connect() as db:
            return [dict(r) for r in db.execute('SELECT * FROM hourly_candles WHERE hour>=? AND hour<? ORDER BY venue,base,hour', (start, end))]

    def info(self) -> dict:
        with self.lock:
            return dict(self.summary, enabled=True, file='data/index-hours.sqlite3', historical_backfill=False)


class HourlyCollector:
    def __init__(self, engine, fetch, wall=time.time):
        self.engine, self.archive, self.fetch = engine, engine.archive, fetch
        self.wall = wall
        self.wall_offset = wall() - engine.clock()
        self.clock_lock = threading.Lock()
        self.floor = (int(engine.clock()) // HOUR - 1) * HOUR
        self.last = {}

    def now(self):
        # Some OS monotonic clocks pause during sleep. Recheck official UTC
        # before another request so waking a Mac cannot start old-hour reads.
        with self.clock_lock:
            if abs((self.wall() - self.engine.clock()) - self.wall_offset) > 5:
                if self.engine.clock_sync is None:
                    raise ValueError('Collection clock changed; restart to verify UTC')
                self.engine.clock_sync()
                if not self.engine.clock_info or self.engine.clock_info().get('mode') != 'server':
                    raise ValueError('Collection UTC could not be verified after sleep or a PC clock edit')
                self.wall_offset = self.wall() - self.engine.clock()
            return self.engine.clock()

    def collect_once(self, venue: str):
        now = self.now()
        start = (int(now) // HOUR - 1) * HOUR
        if start < self.floor or now < start + HOUR + PUBLICATION_DELAY or self.last.get(venue, 0) >= start:
            return
        with self.engine.lock:
            snapshot = self.engine.tickers[venue]
            markets = dict(snapshot.markets)
            ready = bool(markets) and not snapshot.error and 0 <= now - snapshot.at <= 30
        if not ready:
            return
        self.last[venue] = start
        if not self.archive.begin(venue, start, markets):
            return  # Includes interrupted/failed windows: no restart replay.
        try:
            if venue not in SUPPORTED:
                return
            for base in sorted(markets):
                if self.engine.stop.is_set() or int(self.now()) // HOUR - 1 != start // HOUR:
                    break  # Skip elapsed windows after suspend or a long request.
                try:
                    payload = self.fetch(request_url(venue, base, start))
                    candle = parse_candle(venue, base, start, payload)
                    if int(self.now()) // HOUR - 1 != start // HOUR:
                        break  # A request spanning suspend must not fill an old hour.
                    self.archive.save(venue, base, start, candle)
                except Exception as exc:
                    self.archive.save(venue, base, start, None, str(exc))
                    LOG.warning('%s %s current hour %s: %s', venue, base, start, exc)
                    # Provider throttling/server failures stop this hour rather
                    # than hammering every remaining market or retrying history.
                    if getattr(exc, 'code', 0) in (418, 429) or getattr(exc, 'code', 0) >= 500:
                        break
                if self.engine.stop.wait(0.25):
                    break
        finally:
            self.archive.finish(venue, start)
            LOG.info('%s current-hour collection finished: %s (no historical retry)', venue, start)

    def run(self, venue: str):
        while not self.engine.stop.is_set():
            try:
                self.collect_once(venue)
            except Exception as exc:
                with self.archive.lock:
                    self.archive.summary['error'] = str(exc)[:300]
                LOG.exception('%s current-hour worker failed', venue)
            # Registries/tickers initialize asynchronously. Start promptly once
            # they arrive, then use the normal low-frequency hour scheduler.
            self.engine.stop.wait(1 if venue not in self.last else 10)
