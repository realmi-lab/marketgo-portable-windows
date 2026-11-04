"""Shared Windows/macOS loopback server with a persistent hourly index."""
from __future__ import annotations

import argparse
import json
import logging
from logging.handlers import RotatingFileHandler
import mimetypes
from pathlib import Path
import sys
import threading
import webbrowser
import uuid
from decimal import Decimal
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / 'vendor'))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from engine import Engine  # noqa: E402
from collector import Collector, tls_context, get_json  # noqa: E402
from market_clock import MarketClock  # noqa: E402
from hourly import HourlyArchive  # noqa: E402
from notices import NoticeFeed, NoticeUnavailable  # noqa: E402

LOG = logging.getLogger('marketgo')


def encode(value):
    if isinstance(value, Decimal):
        return float(value)
    raise TypeError(f'Not JSON serializable: {type(value)}')


class Server(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = False

    def __init__(self, address, engine, web, notices=None):
        self.engine, self.web = engine, web
        self.notices = notices
        self.instance_id = uuid.uuid4().hex
        super().__init__(address, Handler)


class Handler(SimpleHTTPRequestHandler):
    extensions_map = {**SimpleHTTPRequestHandler.extensions_map, '.js': 'text/javascript',
                      '.css': 'text/css', '.woff2': 'font/woff2', '.svg': 'image/svg+xml'}
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=str(args[2].web), **kwargs)

    def log_message(self, fmt, *args):
        if len(args) >= 2 and str(args[1]) not in ('200', '304'):
            LOG.info('HTTP: ' + fmt, *args)

    def end_headers(self):
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        super().end_headers()

    def send_json(self, payload, status=200):
        body = json.dumps(payload, ensure_ascii=False, separators=(',', ':'), default=encode, allow_nan=False).encode('utf-8')
        self.send_response(status)
        self.send_header('Content-Type', 'application/json; charset=utf-8')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def list_directory(self, _path):
        self.send_error(404)
        return None

    def do_GET(self):
        port = self.server.server_address[1]
        if self.headers.get('Host') not in (f'127.0.0.1:{port}', f'localhost:{port}'):
            self.send_json({'detail': 'Local host only'}, 403)
            return
        parsed = urlsplit(self.path)
        query = {k: values[-1] for k, values in parse_qs(parsed.query, keep_blank_values=True).items()}
        engine = self.server.engine
        try:
            if parsed.path == '/api/portable/status':
                self.send_json({'engine': 'python', 'version': '3.3.0', 'notices': bool(self.server.notices),
                                'instance_id': self.server.instance_id,
                                'hourly': engine.archive.info() if engine.archive else None,
                                'clock': engine.clock_info() if engine.clock_info else None})
            elif parsed.path == '/api/portable/state':
                self.send_json(engine.state(query))
            elif parsed.path == '/api/market-share/index-day':
                payload = engine.index_day(query)
                if query.get('projection') == 'domestic_grid':
                    payload = {k: payload[k] for k in ('day', 'status', 'storage_mode', 'domestic')}
                    payload['contract_version'] = 'INDEX_DAY_GRID_V1'
                self.send_json(payload)
            elif parsed.path == '/api/market-share/realtime-index':
                self.send_json(engine.realtime_index(query))
            elif parsed.path == '/api/market-share/index-filters':
                self.send_json(engine.filters())
            elif parsed.path == '/api/market-share/notices' and self.server.notices:
                self.send_json(self.server.notices.archive(query))
            elif parsed.path == '/api/market-share/notice-portal' and self.server.notices:
                self.send_json(engine.notice_portal(self.server.notices.timeline()))
            elif parsed.path.startswith('/api/'):
                self.send_json({'detail': 'Unsupported API'}, 404)
            else:
                if parsed.path in ('/notices', '/notices/'):
                    self.path = '/index.html'
                # SimpleHTTPRequestHandler normalizes traversal and serves only
                # the bundled web tree; the package contains no symlink assets.
                super().do_GET()
        except (ConnectionError, BrokenPipeError, TimeoutError):
            pass
        except ValueError as exc:
            self.send_json({'detail': str(exc)}, 400)
        except NoticeUnavailable as exc:
            self.send_json({'detail': str(exc)}, exc.status)
        except Exception:
            LOG.exception('HTTP request failed')
            self.send_json({'detail': 'Local server error; see logs/runtime.log'}, 503)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--port', type=int, default=20560)
    parser.add_argument('--no-browser', action='store_true')
    args = parser.parse_args()
    logs = ROOT / 'logs'
    logs.mkdir(exist_ok=True)
    LOG.setLevel(logging.INFO)
    formatter = logging.Formatter('%(asctime)s %(levelname)s %(threadName)s %(message)s')
    for handler in (RotatingFileHandler(logs / 'runtime.log', maxBytes=2_000_000, backupCount=1, encoding='utf-8'),
                    logging.StreamHandler(sys.stdout)):
        handler.setFormatter(formatter)
        LOG.addHandler(handler)
    web = ROOT / 'web'
    if not (web / 'index.html').is_file():
        LOG.error('Missing web/index.html. Extract the entire ZIP before starting.')
        return 1
    try:
        server = Server(('127.0.0.1', args.port), None, web)
    except OSError as exc:
        LOG.error('Cannot open local port %s. Close the older MarketGO execution window. %s', args.port, exc)
        return 1
    collector = None
    try:
        context = tls_context()
        clock = MarketClock()
        clock.synchronize(context)
        archive = HourlyArchive(ROOT / 'data' / 'index-hours.sqlite3')
        engine = Engine(clock=clock.time, clock_info=clock.info, archive=archive, clock_sync=lambda: clock.synchronize(context))
        server.engine = engine
        server.notices = NoticeFeed(lambda url: get_json(url, context))
        collector = Collector(engine, context)
        collector.start()
        url = f'http://127.0.0.1:{args.port}/'
        LOG.info('MarketGO Python %s; %s; hourly index ON; historical backfill OFF; public notice feed ON; local LLM OFF', sys.version.split()[0], url)
        if not args.no_browser:
            timer = threading.Timer(0.7, lambda: webbrowser.open(url))
            timer.daemon = True
            timer.start()
        server.serve_forever(poll_interval=0.25)
    except KeyboardInterrupt:
        LOG.info('Stopping. Realtime history is discarded; data/index-hours.sqlite3 is retained.')
    finally:
        if collector:
            collector.close()
        server.server_close()
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
