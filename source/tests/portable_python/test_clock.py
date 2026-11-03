from datetime import datetime, timezone
from decimal import Decimal
from email.utils import format_datetime
import ssl
import unittest
from unittest.mock import patch

from adapters import Trade, ensure_fresh
from engine import Engine, day_at
from market_clock import MarketClock, Sample, https_sample

UTC = 1793607000.25


class ClockTests(unittest.TestCase):
    def calibrated(self, offset):
        wall, mono = [UTC + offset], [100.0]
        clock = MarketClock(lambda: wall[0], lambda: mono[0])
        def fetch(name, _url, _context, monotonic):
            return Sample(name, UTC + (300 if name == 'digitalx' else 0), monotonic())
        self.assertTrue(clock.synchronize(None, fetch))
        return clock, wall, mono

    def test_positive_and_negative_pc_clock_offsets_do_not_reject_valid_trades(self):
        for offset in (32400, -32400, 180, -600):
            with self.subTest(offset=offset):
                clock, wall, mono = self.calibrated(offset)
                self.assertAlmostEqual(clock.time(), UTC)
                self.assertAlmostEqual(clock.info()['offset_seconds'], -offset)
                self.assertEqual(wall[0], UTC + offset)  # OS wall clock was untouched.
                engine = Engine(clock.time, clock_info=clock.info)
                engine.set_expected('bithumb', {'b'})
                engine.feed_status('b','bithumb','WAITING')
                trade=Trade('BTC','one',UTC,Decimal(6))
                self.assertTrue(engine.accept('b','bithumb',trade,{'BTC'}))
                self.assertFalse(engine.accept('b','bithumb',Trade('BTC','future',UTC+6,Decimal(1)),{'BTC'}))
                ensure_fresh('bithumb',{'data':{'date':str(int(UTC*1000))}},{'BTC':'KRW-BTC'},clock.time())
                mono[0] += 1.5
                self.assertEqual(engine.realtime_index({'window':'10'})['domestic']['totals']['domestic']['total'],Decimal(6))
                self.assertEqual(engine.state({'window':'10'})['clock']['mode'],'server')
                self.assertNotEqual(day_at(wall[0]),day_at(clock.time())) if offset==32400 else None

    def test_manual_pc_clock_edit_does_not_move_collection_time(self):
        clock, wall, mono = self.calibrated(0)
        wall[0] += 86400
        mono[0] += 30
        self.assertAlmostEqual(clock.time(), UTC+30)
        self.assertAlmostEqual(clock.info()['offset_seconds'], -86370)

    def test_single_authority_and_tls_failures_cannot_set_reference_time(self):
        clock=MarketClock(lambda:UTC+600,lambda:5)
        def fetch(name,*args):
            if name=='bithumb':return Sample(name,UTC,5)
            raise ssl.SSLCertVerificationError('injected certificate failure')
        self.assertFalse(clock.synchronize(None,fetch))
        self.assertEqual(clock.info()['mode'],'local')
        self.assertIn('Windows time',clock.info()['error'])
        self.assertEqual(clock.time(),UTC+600)

    def test_disagreeing_https_clocks_do_not_form_a_false_reference(self):
        clock=MarketClock(lambda:UTC,lambda:5)
        values=iter([UTC,UTC+60,UTC+120,UTC+180])
        self.assertFalse(clock.synchronize(None,lambda name,*args:Sample(name,next(values),5)))
        self.assertEqual(clock.info()['mode'],'local')

    def test_https_headers_are_used_without_downgrading_tls_or_redirecting_authority(self):
        class Response:
            def __init__(self,url,headers):self.url=url;self.headers=headers;self.closed=False
            def geturl(self):return self.url
            def close(self):self.closed=True
        url='https://api.example.test/clock'
        response=Response(url,{'Date':format_datetime(datetime.fromtimestamp(UTC,timezone.utc),usegmt=True),'Age':'0'})
        ticks=iter([1,1.2])
        with patch('market_clock.urllib.request.urlopen',return_value=response) as opener:
            sample=https_sample('test',url,'verified-context',lambda:next(ticks))
        self.assertAlmostEqual(sample.utc_at_receipt,int(UTC)+0.6)
        self.assertEqual(opener.call_args.kwargs['context'],'verified-context')
        self.assertTrue(response.closed)
        for redirect,age in [('http://api.example.test/clock','0'),('https://other.example/clock','0'),(url,'121')]:
            response=Response(redirect,{'Date':format_datetime(datetime.fromtimestamp(UTC,timezone.utc),usegmt=True),'Age':age})
            ticks=iter([1,1.2])
            with patch('market_clock.urllib.request.urlopen',return_value=response), self.assertRaises(ValueError):
                https_sample('test',url,'verified-context',lambda:next(ticks))


if __name__=='__main__':unittest.main(verbosity=2)
