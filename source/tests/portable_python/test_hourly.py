from datetime import datetime, timezone
from decimal import Decimal as D
import http.client
import json
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch
from urllib.parse import parse_qs, urlsplit

from adapters import VENUES
from collector import Collector
from engine import Engine, day_at
from hourly import HourlyArchive, HourlyCollector, parse_candle, request_url
from server import Server

START = int(datetime(2026, 11, 2, 14, tzinfo=timezone.utc).timestamp())  # 23:00 KST


def payload(venue, start=START, amount='12.34567890123456789', base='BTC'):
    if venue == 'coinone':
        return dict(result='success', error_code='0', chart=[dict(timestamp=start*1000, open='10', quote_volume=amount, target_volume='2')])
    return [dict(market=f'KRW-{base}', unit=60, candle_date_time_utc=datetime.fromtimestamp(start, timezone.utc).strftime('%Y-%m-%dT%H:%M:%S'),
                 opening_price='10', candle_acc_trade_price=amount, candle_acc_trade_volume='2')]


class Hourly(unittest.TestCase):
    def test_exact_hour_parsing_and_request_bounds(self):
        for venue in ('bithumb', 'upbit', 'coinone'):
            query = parse_qs(urlsplit(request_url(venue, 'BTC', START)).query)
            self.assertEqual(query.get('count', query.get('size')), ['1'])
            self.assertEqual(parse_candle(venue, 'BTC', START, payload(venue))['amount'], D('12.34567890123456789'))
            self.assertEqual(parse_candle(venue, 'BTC', START, payload(venue, amount='0'))['amount'], D(0))
            self.assertIsNone(parse_candle(venue, 'BTC', START, payload(venue, START-3600)))
            self.assertIsNone(parse_candle(venue, 'BTC', START, payload(venue, START+3600)))
            with self.assertRaises(ValueError):
                parse_candle(venue, 'BTC', START, payload(venue, amount='NaN'))
        self.assertEqual(parse_qs(urlsplit(request_url('upbit', 'BTC', START)).query)['to'], ['2026-11-02T15:00:00Z'])
        self.assertEqual(parse_qs(urlsplit(request_url('bithumb', 'BTC', START)).query)['to'], ['2026-11-03T00:00:00'])
        self.assertEqual(parse_qs(urlsplit(request_url('coinone', 'BTC', START)).query)['timestamp'], [str((START+3600)*1000-1)])
        bad = payload('bithumb'); bad[0]['market'] = 'KRW-ETH'
        with self.assertRaises(ValueError): parse_candle('bithumb', 'BTC', START, bad)
        bad = payload('upbit'); bad[0]['unit'] = 1
        with self.assertRaises(ValueError): parse_candle('upbit', 'BTC', START, bad)
        with self.assertRaises(ValueError): parse_candle('coinone', 'BTC', START, {'result': 'error', 'chart': []})
        with self.assertRaises(ValueError): request_url('gopax', 'BTC', START)

    def test_automatic_latest_hour_once_restart_and_downtime_skip(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)/'hours.sqlite3'
            clock = [START+3600+119]
            archive = HourlyArchive(path)
            engine = Engine(lambda: clock[0], archive=archive)
            engine.update_ticker('bithumb', {'BTC': 'KRW-BTC'}, {'BTC': D(500)})
            with patch.object(engine.stop, 'wait', return_value=False):
                fetch = unittest.mock.Mock(return_value=payload('bithumb'))
                worker = HourlyCollector(engine, fetch, wall=engine.clock)
                worker.collect_once('bithumb')
                fetch.assert_not_called()
                clock[0] += 1
                worker.collect_once('bithumb'); worker.collect_once('bithumb')
                self.assertEqual(fetch.call_count, 1)
                # A restarted process must not re-request an attempted hour.
                restarted = Engine(lambda: clock[0], archive=HourlyArchive(path))
                restarted.update_ticker('bithumb', {'BTC':'KRW-BTC'}, {'BTC':D(500)})
                HourlyCollector(restarted, fetch).collect_once('bithumb')
                self.assertEqual(fetch.call_count, 1)
                # Simulate sleep. Only the latest closed hour is requested.
                clock[0] += 4*3600
                engine.update_ticker('bithumb', {'BTC':'KRW-BTC'}, {'BTC':D(500)})
                fetch.return_value = payload('bithumb', START+4*3600)
                worker.collect_once('bithumb')
                self.assertEqual(fetch.call_count, 2)
                self.assertEqual([r['hour'] for r in archive.snapshot(START, START+5*3600)], [START, START+4*3600])

    def test_sleep_rechecks_utc_and_does_not_save_an_elapsed_request(self):
        with tempfile.TemporaryDirectory() as directory:
            wall, clock = [START+4000], [START+4000]
            def sync():
                clock[0] = wall[0]
            engine = Engine(lambda:clock[0], archive=HourlyArchive(Path(directory)/'h.sqlite3'),
                            clock_info=lambda:{'mode':'server'}, clock_sync=sync)
            engine.update_ticker('upbit',{'BTC':'KRW-BTC'},{'BTC':D(10)})
            def fetch(url):
                wall[0] += 4*3600  # OS wall includes sleep; process monotonic does not.
                return payload('upbit')
            with patch.object(engine.stop,'wait',return_value=False):
                worker = HourlyCollector(engine,fetch,wall=lambda:wall[0])
                worker.collect_once('upbit')
            self.assertEqual(clock[0],wall[0])
            self.assertEqual(engine.archive.info()['stored_candles'],0)
            self.assertEqual(engine.archive.snapshot(START,START+3600)[0]['status'],'MISSING')

    def test_failed_or_interrupted_hours_are_not_retried_or_zero_filled(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = HourlyArchive(Path(directory)/'hours.sqlite3')
            engine = Engine(lambda: START+4000, archive=archive)
            for venue in VENUES:
                engine.update_ticker(venue, {'BTC': 'KRW-BTC'}, {'BTC': D(500)})
            fetch = unittest.mock.Mock(side_effect=TimeoutError('offline'))
            with patch.object(engine.stop, 'wait', return_value=False):
                worker = HourlyCollector(engine, fetch)
                worker.collect_once('bithumb')
                self.assertEqual(fetch.call_count, 1)
                HourlyCollector(engine, fetch).collect_once('bithumb')
                self.assertEqual(fetch.call_count, 1)
                worker.collect_once('korbit'); worker.collect_once('gopax')
                self.assertEqual(fetch.call_count, 1)
            archive.begin('upbit', START, {'BTC': 'KRW-BTC'})  # interrupted before HTTP
            reopened = HourlyArchive(archive.path)
            self.assertEqual(next(r for r in reopened.snapshot(START,START+3600) if r['venue']=='upbit')['status'], 'MISSING')
            HourlyCollector(engine, fetch).collect_once('upbit')
            self.assertEqual(fetch.call_count, 1)
            result = engine.index_day({'day':day_at(engine.clock())})
            self.assertEqual(result['status'], 'PARTIAL')
            for venue in ('bithumb', 'upbit', 'korbit', 'gopax'):
                self.assertIsNone(result['domestic']['totals'][venue]['total'])

    def test_kst_day_storage_restart_readonly_and_ticker_separation(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)/'hours.sqlite3'
            archive = HourlyArchive(path)
            for venue, amount in [('bithumb','10'), ('upbit','30'), ('coinone','0'), ('gopax',None)]:
                archive.begin(venue, START, {'BTC':'KRW-BTC'})
                if amount is not None:
                    archive.save(venue, 'BTC', START, parse_candle(venue, 'BTC', START, payload(venue, amount=amount)))
                archive.finish(venue, START)
            archive.begin('bithumb', START+3600, {'BTC':'KRW-BTC'})
            archive.save('bithumb', 'BTC', START+3600, parse_candle('bithumb','BTC',START+3600,payload('bithumb',START+3600,amount='999')))
            archive.finish('bithumb', START+3600)
            engine = Engine(lambda: START+7200+120, archive=HourlyArchive(path))
            result = engine.index_day({'day':'2026-11-02'})
            self.assertEqual(result['contract_version'], 'INDEX_DAY_HOURLY_V1')
            self.assertEqual(result['domestic']['totals']['domestic']['total'],D(40))
            self.assertEqual(result['domestic']['totals']['bithumb']['share'],D(25))
            self.assertEqual(result['domestic']['totals']['coinone']['total'],D(0))
            self.assertIsNone(result['domestic']['totals']['gopax']['total'])
            self.assertTrue(result['domestic']['totals']['domestic']['partial'])
            self.assertEqual(engine.index_day({'day':'2026-11-02','assets':'BTC','venue':'bithumb'})['domestic']['totals']['domestic']['total'], D(10))
            self.assertEqual(engine.index_day({'day':'2026-11-02','quote':'btc'})['domestic']['coins'], [])
            self.assertEqual(engine.index_day({'day':'2026-11-01'})['rows'], [])
            self.assertEqual(engine.archive.info()['stored_candles'],4)
            engine.update_ticker('bithumb', {'BTC':'KRW-BTC'}, {'BTC':D(5000)})
            ticker = engine.index_day({'source':'ticker_24h'})
            self.assertEqual(ticker['storage_mode'], 'TICKER_24H')
            self.assertEqual(ticker['domestic']['totals']['bithumb']['total'], D(5000))
            with self.assertRaises(ValueError): engine.index_day({'day':'2026-02-30'})
            with self.assertRaises(ValueError): engine.index_day({'day':'2026-11-04'})

    def test_hourly_workers_are_started_for_each_venue(self):
        with tempfile.TemporaryDirectory() as directory:
            engine = Engine(lambda: START+4000, archive=HourlyArchive(Path(directory)/'h.sqlite3'))
            with patch('collector.threading.Thread') as thread:
                Collector(engine, context=object()).start()
            names = [c.kwargs.get('name') for c in thread.call_args_list]
            self.assertEqual(sorted(n for n in names if n.startswith('hourly-')), sorted('hourly-'+v for v in VENUES))

    def test_http_history_projection_and_status(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); web = root/'web'; web.mkdir()
            archive = HourlyArchive(root/'data'/'index-hours.sqlite3')
            archive.begin('upbit',START,{'BTC':'KRW-BTC'})
            archive.save('upbit','BTC',START,parse_candle('upbit','BTC',START,payload('upbit')))
            archive.finish('upbit',START)
            engine = Engine(lambda: START+7200, archive=archive)
            server = Server(('127.0.0.1',0),engine,web)
            thread = threading.Thread(target=server.serve_forever,daemon=True); thread.start()
            try:
                def get(path):
                    with closing_connection(server.server_port) as connection:
                        connection.request('GET',path)
                        response = connection.getresponse()
                        self.assertEqual(response.status,200)
                        return json.loads(response.read())
                self.assertEqual(get('/api/portable/status')['hourly']['stored_candles'],1)
                self.assertEqual(get('/api/market-share/index-day?day=2026-11-02')['rows'][0]['amount_hours'],1)
                grid = get('/api/market-share/index-day?day=2026-11-02&projection=domestic_grid')
                self.assertEqual(grid['contract_version'],'INDEX_DAY_GRID_V1')
                self.assertAlmostEqual(grid['domestic']['totals']['upbit']['total'],12.34567890123456789)
                self.assertEqual(get('/api/market-share/index-day?source=ticker_24h')['contract_version'],'INDEX_TICKER_V1')
            finally:
                server.shutdown(); server.server_close(); thread.join(2)


def closing_connection(port):
    from contextlib import closing
    return closing(http.client.HTTPConnection('127.0.0.1',port,timeout=2))
