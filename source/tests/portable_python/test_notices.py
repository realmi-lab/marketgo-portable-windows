from decimal import Decimal as D
import http.client
import json
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import Mock
from urllib.error import HTTPError

from adapters import VENUES, Trade, trades
from engine import Engine
from notices import NoticeFeed, NoticeUnavailable, ORIGIN
from server import Server

NOW = 1793756000.5
ROLLING = {'as_of_ms': 1793756000000, 'evidence': {'domestic_notices': [{'title': '공지 일정'}]}}
PAGE = {'snapshot': {'version': 7, 'items': [{'notice_id': '1', 'title': '공지'}]},
        'total': 1, 'offset': 0, 'limit': 100, 'as_of_ms': 1793756000000, 'from_ms': 1791164000000}


class Notices(unittest.TestCase):
    def test_archive_version_pagination_and_bounded_cache(self):
        fetch = Mock(return_value=PAGE)
        clock = [10]
        feed = NoticeFeed(fetch, lambda: clock[0])
        query = dict(offset='0', limit='100', version='7', as_of_ms='1793756000000')
        first = feed.archive(query)
        first['snapshot']['items'].clear()
        self.assertEqual(len(feed.archive(query)['snapshot']['items']), 1)
        fetch.assert_called_once_with(ORIGIN + '/api/market-share/notices?offset=0&limit=100&version=7&as_of_ms=1793756000000')
        clock[0] += 61
        feed.archive(query)
        self.assertEqual(fetch.call_count, 2)
        for query in ({'limit':'101'}, {'offset':'-1'}, {'fields':'body'}, {'version':'https://example.com'}):
            with self.assertRaises(ValueError):
                feed.archive(query)

    def test_failure_is_not_empty_success_and_conflicts_are_preserved(self):
        for status, expected in ((409, 409), (429, 429), (500, 502)):
            feed = NoticeFeed(Mock(side_effect=HTTPError(ORIGIN, status, 'failed', {}, None)))
            with self.assertRaises(NoticeUnavailable) as result:
                feed.archive({})
            self.assertEqual(result.exception.status, expected)
        with self.assertRaises(NoticeUnavailable):
            NoticeFeed(lambda _: {}).timeline()

    def test_version_conflict_evicts_cached_first_page_before_retry(self):
        updated = {**PAGE, 'snapshot': {**PAGE['snapshot'], 'version': 8}}
        fetch = Mock(side_effect=[PAGE, HTTPError(ORIGIN,409,'changed',{},None), updated])
        feed = NoticeFeed(fetch)
        self.assertEqual(feed.archive({'limit':'100'})['snapshot']['version'], 7)
        with self.assertRaises(NoticeUnavailable):
            feed.archive({'offset':'100', 'version':'7'})
        self.assertEqual(feed.archive({'limit':'100'})['snapshot']['version'], 8)
        self.assertEqual(fetch.call_count, 3)

    def engine(self):
        engine = Engine(lambda: NOW)
        engine.oldest = int(NOW) - 1000
        for venue in VENUES:
            engine.set_expected(venue, {venue})
            engine.feed_status(venue, venue, 'WAITING')
            engine.feeds[venue].since = NOW - 700
        return engine

    def test_local_amount_quantity_dedup_closed_second_and_gap(self):
        engine = self.engine()
        for venue in VENUES:
            trade = Trade('BTC', 'one', int(NOW)-1, D(6), D(3))
            self.assertTrue(engine.accept(venue, venue, trade, {'BTC'}))
            self.assertFalse(engine.accept(venue, venue, trade, {'BTC'}))
        engine.accept('bithumb', 'bithumb', Trade('BTC', 'open', int(NOW), D(999), D(999)), {'BTC'})
        result = engine.notice_portal(ROLLING)
        self.assertEqual(result['assets'], [dict(asset='BTC', domestic_volume_krw=D(30),
                         domestic_trade_count=5, domestic_base_quantity=D(15))])
        self.assertTrue(result['context']['selected_window_complete'])
        self.assertEqual(result['context']['current_interval_end'] - result['context']['current_interval_start'], 600)
        engine.feed_status('upbit', 'upbit', 'ERROR', 'disconnected')
        self.assertFalse(engine.notice_portal(ROLLING)['context']['selected_window_complete'])
        self.assertEqual(engine.notice_portal(ROLLING)['assets'][0]['domestic_volume_krw'], D(30))

    def test_missing_quantity_and_empty_history_are_not_fabricated(self):
        engine = self.engine()
        self.assertEqual(engine.notice_portal(ROLLING)['assets'], [])
        engine.accept('bithumb', 'bithumb', Trade('BTC', 'old-format', int(NOW)-1, D(6)), {'BTC'})
        self.assertIsNone(engine.notice_portal(ROLLING)['assets'][0]['domestic_base_quantity'])
        frame = dict(type='trade', code='KRW-BTC', sequential_id=1, trade_timestamp=int(NOW*1000),
                     trade_price=2, trade_volume=3)
        self.assertEqual(trades('upbit', frame)[0].quantity, D(3))

    def test_cached_schedule_keeps_market_values_local_and_current(self):
        fetch = Mock(return_value=dict(rolling_24h=ROLLING, assets=[{'asset':'BTC','domestic_volume_krw':999999}]))
        feed, engine = NoticeFeed(fetch), self.engine()
        self.assertEqual(engine.notice_portal(feed.timeline())['assets'], [])
        engine.accept('bithumb', 'bithumb', Trade('ETH', 'one', int(NOW)-1, D(8), D(4)), {'ETH'})
        result = engine.notice_portal(feed.timeline())
        self.assertEqual(result['assets'][0]['asset'], 'ETH')
        self.assertEqual(result['assets'][0]['domestic_volume_krw'], D(8))
        self.assertEqual(fetch.call_count, 1)

    def test_http_navigation_archive_timeline_and_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            web = Path(directory)
            (web/'index.html').write_text('<html>portable frontend</html>')
            fetch = Mock(side_effect=lambda url: PAGE if '/notices?' in url else {'rolling_24h':ROLLING})
            server = Server(('127.0.0.1',0), self.engine(), web, NoticeFeed(fetch))
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                for path in ('/notices/', '/notices?source=menu', '/api/portable/status',
                             '/api/market-share/notices?limit=100', '/api/market-share/notice-portal'):
                    conn = http.client.HTTPConnection(*server.server_address, timeout=2)
                    conn.request('GET', path)
                    response = conn.getresponse()
                    data = response.read()
                    self.assertEqual(response.status, 200, path)
                    if path.startswith('/notices'):
                        self.assertIn(b'portable frontend', data)
                    if path.endswith('/status'):
                        self.assertTrue(json.loads(data)['notices'])
                    conn.close()
                server.notices = NoticeFeed(Mock(side_effect=TimeoutError()))
                conn = http.client.HTTPConnection(*server.server_address, timeout=2)
                conn.request('GET', '/api/market-share/notice-portal')
                self.assertEqual(conn.getresponse().status, 502)
                conn.close()
            finally:
                server.shutdown()
                server.server_close()
                thread.join(2)
