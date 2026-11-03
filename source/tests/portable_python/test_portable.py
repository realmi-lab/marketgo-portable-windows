import contextlib
from decimal import Decimal as D
import http.client
import json
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch

from adapters import VENUES, Trade, ensure_fresh, number, registry, subscriptions, tickers, trades
from engine import Engine, day_at, grid, row
from collector import Collector, TradeWorker, abort_socket
from server import Server

NOW = 1793607000.5


class Accounting(unittest.TestCase):
    def engine(self, **kwargs):
        clock = [NOW]
        engine = Engine(lambda: clock[0], **kwargs)
        engine.oldest = int(NOW) - 120
        for venue in VENUES:
            engine.update_ticker(venue, {'BTC': 'KRW-BTC', 'ETH': 'KRW-ETH'}, {'BTC': D(100), 'ETH': D(50)})
            engine.set_expected(venue, {venue})
            engine.feed_status(venue, venue, 'WAITING')
            engine.feeds[venue].since = NOW - 60
        return engine, clock

    def test_dedup_closed_seconds_and_coin_denominators(self):
        engine, _ = self.engine()
        for index, venue in enumerate(VENUES):
            self.assertTrue(engine.accept(venue, venue, Trade('BTC', 'same-id', int(NOW)-1, D(index+1)), {'BTC','ETH'}))
            self.assertFalse(engine.accept(venue, venue, Trade('BTC', 'same-id', int(NOW)-1, D(index+1)), {'BTC','ETH'}))
            self.assertTrue(engine.accept(venue, venue, Trade('ETH', 'same-id', int(NOW)-1, D(10-index)), {'BTC','ETH'}))
        # An open-second fill must not enter the latest closed-second query.
        engine.accept('bithumb','bithumb',Trade('BTC','open',NOW,D(999)),{'BTC'})
        result = engine.realtime_index({'window':'10'})
        self.assertEqual(result['domestic']['totals']['domestic']['total'], D(55))
        btc = next(c for c in result['domestic']['coins'] if c['base']=='BTC')
        eth = next(c for c in result['domestic']['coins'] if c['base']=='ETH')
        self.assertAlmostEqual(float(btc['cells']['bithumb']['share']), 100/15)
        self.assertAlmostEqual(float(eth['cells']['bithumb']['share']), 25)
        self.assertEqual(sum(c['cells']['domestic']['total'] for c in result['domestic']['coins']), D(55))

    def test_filter_zero_null_and_snapshot_time(self):
        rows=[row('bithumb','BTC',D(0),False,'T'),row('korbit','BTC',D(40),False,'T'),row('upbit','BTC',None,True,'T')]
        result=grid(rows,{})
        self.assertEqual(result['totals']['bithumb']['total'],D(0))
        self.assertIsNone(result['totals']['upbit']['total'])
        self.assertTrue(result['totals']['domestic']['partial'])
        self.assertEqual(grid(rows,{'venue':'korbit'})['totals']['korbit']['share'],D(100))
        self.assertEqual(grid(rows,{'assets':''})['coins'],[])
        self.assertEqual(grid(rows,{'quote':'btc'})['coins'],[])
        engine, clock=self.engine()
        self.assertEqual(engine.ticker_index({})['status'],'COMPLETE')
        clock[0]+=31
        self.assertEqual(engine.ticker_index({})['status'],'PARTIAL')
        self.assertIsNone(engine.ticker_index({})['domestic']['totals']['korbit']['total'])
        with self.assertRaisesRegex(ValueError,'archived hourly'):
            engine.ticker_index({'day':'2020-01-01'})

    def test_reconnect_keeps_gap_partial_and_replays_excluded(self):
        engine, clock=self.engine()
        trade=Trade('BTC','one',int(NOW)-1,D(5))
        engine.accept('bithumb','bithumb',trade,{'BTC'})
        engine.feed_status('bithumb','bithumb','ERROR','connection lost')
        self.assertFalse(engine.realtime_index({'window':'10'})['window_complete'])
        clock[0]+=2
        engine.feed_status('bithumb','bithumb','CONNECTING')
        engine.feed_status('bithumb','bithumb','WAITING')
        self.assertFalse(engine.accept('bithumb','bithumb',trade,{'BTC'}))
        self.assertTrue(engine.accept('bithumb','bithumb',Trade('BTC','two',int(clock[0])-1,D(7)),{'BTC'}))
        self.assertFalse(engine.realtime_index({'window':'10'})['window_complete'])
        self.assertEqual(engine.realtime_index({'window':'10'})['domestic']['totals']['bithumb']['total'],D(12))

    def test_history_memory_cap_and_restart(self):
        engine, clock=self.engine(max_records=2)
        for i in range(4):
            clock[0]=NOW+i
            engine.accept('bithumb','bithumb',Trade('BTC',str(i),int(clock[0]),D(1)),{'BTC'})
        self.assertLessEqual(engine.records,2)
        self.assertGreater(engine.oldest,int(NOW))
        self.assertFalse(engine.realtime_index({'window':'60'})['window_complete'])
        fresh=Engine(lambda:clock[0])
        self.assertEqual(fresh.realtime_index({'window':'10'})['domestic']['coins'],[])

    def test_24h_same_source_no_cached_live_mutation(self):
        engine,_=self.engine()
        live=engine.realtime_index({'window':'86400'})
        state=engine.state({'window':'86400'})
        self.assertEqual(live['data_source'],'OFFICIAL_KRW_24H_TICKER')
        for venue in VENUES:
            value=next(r for r in state['exchanges'] if r['exchange']==venue)
            self.assertEqual(value['volume_krw'],live['domestic']['totals'][venue]['total'])
        self.assertEqual(engine.state({'window':'10'})['exchanges'][0]['volume_krw'],None)
        self.assertIsNone(state['changes'][0]['delta'])

    def test_invalid_trade_and_unlisted_market(self):
        engine,_=self.engine()
        for trade in (Trade('BTC','future',NOW+6,D(1)),Trade('BTC','old',NOW-100,D(1)),Trade('BTC','nan',NOW,D('NaN')),Trade('ETH','unlisted',NOW,D(1))):
            self.assertFalse(engine.accept('bithumb','bithumb',trade,{'BTC'}))


class Adapters(unittest.TestCase):
    def test_five_wire_trade_formats_and_snapshot_exclusion(self):
        frames={
          'bithumb':dict(type='trade',code='KRW-BTC',sequential_id=1,trade_timestamp=int(NOW*1000),trade_price=2,trade_volume=3),
          'upbit':dict(type='trade',code='KRW-BTC',sequential_id=1,trade_timestamp=int(NOW*1000),trade_price=2,trade_volume=3),
          'coinone':dict(response_type='DATA',channel='TRADE',data=dict(quote_currency='KRW',target_currency='BTC',id='1',timestamp=int(NOW*1000),price='2',qty='3')),
          'korbit':dict(type='trade',symbol='btc_krw',snapshot=False,data=[dict(tradeId=1,timestamp=int(NOW*1000),price='2',qty='3')]),
          'gopax':dict(n='PublicTradeEvent',o=dict(tradeId=1,tradingPairName='BTC-KRW',occurredAt=NOW,price=2,baseAmount=3)),
        }
        for venue,frame in frames.items():
            result=trades(venue,frame)
            self.assertEqual(len(result),1,venue)
            self.assertEqual(result[0].amount,D(6),venue)
        frames['korbit']['snapshot']=True
        self.assertEqual(trades('korbit',frames['korbit']),[])
        frames['bithumb']['stream_type']='SNAPSHOT'
        self.assertEqual(trades('bithumb',frames['bithumb']),[])
        self.assertEqual(trades('coinone',dict(response_type='SUBSCRIBED',channel='TRADE',data=dict(quote_currency='KRW',target_currency='BTC'))),[])
        self.assertEqual(trades('coinone',dict(r='DATA',c='TRADE',d=dict(qc='KRW',tc='BTC',i='one',t=int(NOW*1000),p='2',q='3')))[0].amount,D(6))
        with self.assertRaisesRegex(ValueError,'subscription rejected'):
            trades('coinone',dict(r='ERROR',c='TRADE',d={'message':'rejected'}))
        for bad in (None,True,'NaN','Infinity','-1'):
            with self.assertRaises(ValueError):number(bad)

    def test_registries_and_tickers(self):
        samples={
          'bithumb':({'BTC':'KRW-BTC'},{'status':'0000','data':{'BTC':{'acc_trade_value_24H':'10'},'date':str(int(NOW*1000))}}),
          'upbit':({'BTC':'KRW-BTC'},[{'market':'KRW-BTC','acc_trade_price_24h':20,'timestamp':int(NOW*1000)}]),
          'coinone':({'BTC':'KRW-BTC'},{'result':'success','server_time':int(NOW*1000),'tickers':[{'target_currency':'BTC','quote_currency':'krw','quote_volume':'0'}]}),
          'korbit':({'BTC':'btc_krw'},{'success':True,'data':[{'symbol':'btc_krw','quoteVolume':'40','lastTradedAt':int(NOW*1000)}]}),
          'gopax':({'BTC':'BTC-KRW'},[{'tradingPairName':'BTC-KRW','quoteVolume':'50','lastTraded':NOW}]),
        }
        for venue,(markets,payload) in samples.items():
            with self.assertRaisesRegex(ValueError,'not initialized'):
                tickers(venue,payload,{})
            self.assertIn('BTC',tickers(venue,payload,markets))
            ensure_fresh(venue,payload,markets,NOW)
            if venue in ('bithumb','coinone'):
                with self.assertRaises(ValueError):ensure_fresh(venue,payload,markets,NOW+121)
            else:
                # Last execution age is not the response age of a rolling ticker.
                ensure_fresh(venue,payload,markets,NOW+86400)
            with self.assertRaises(ValueError):tickers(venue,payload,{'BTC':markets['BTC'],'ETH':'missing'})
        self.assertEqual(registry('korbit',{'success':True,'data':[{'symbol':'btc_krw','status':'launched'},{'symbol':'old_krw','status':'delisted'}]}),{'BTC':'btc_krw'})
        self.assertEqual(registry('gopax',[{'baseAsset':'BTC','quoteAsset':'KRW','name':'BTC-KRW'},{'baseAsset':'ETH','quoteAsset':'BTC','name':'ETH-BTC'}]),{'BTC':'BTC-KRW'})
        for venue in VENUES:
            self.assertTrue(subscriptions(venue,{'BTC':'BTC-KRW'},'test'))


class Recovery(unittest.TestCase):
    class Socket:
        def __init__(self,fail=False):self.sock=None;self.keep_running=True;self.closed=False;self.fail=fail;self.sent=[]
        def send(self,value):
            if self.fail:raise OSError('injected send failure')
            self.sent.append(value)
        def close(self,**_):self.closed=True

    def test_subscription_send_failure_aborts_for_reconnect(self):
        engine=Engine(lambda:NOW);engine.set_expected('bithumb',{'b'})
        worker=TradeWorker(engine,'bithumb','b',{'BTC':'KRW-BTC'},None)
        socket=self.Socket(True)
        worker.on_open(socket)
        self.assertTrue(socket.closed)
        self.assertEqual(engine.feeds['b'].state,'ERROR')
        self.assertIn('send failure',engine.feeds['b'].error)

    def test_protocol_heartbeat_and_bad_trade(self):
        engine=Engine(lambda:NOW);engine.set_expected('gopax',{'g'})
        worker=TradeWorker(engine,'gopax','g',{'BTC':'BTC-KRW'},None)
        socket=self.Socket()
        worker.on_message(socket,'"primus::ping::123"')
        self.assertEqual(socket.sent,['"primus::pong::123"'])
        worker.on_message(socket,'{"n":"PublicTradeEvent","o":{}}')
        self.assertTrue(socket.closed)
        self.assertEqual(engine.feeds['g'].state,'ERROR')

    def test_first_registry_fetch_before_five_minutes_of_windows_uptime(self):
        engine=Engine(lambda:NOW)
        update=engine.update_ticker
        def store_and_stop(*args,**kwargs):
            update(*args,**kwargs)
            engine.stop.set()
        responses=[
            [{'market':'KRW-BTC'}],
            {'status':'0000','data':{'BTC':{'acc_trade_value_24H':'10'},'date':str(int(NOW*1000))}},
        ]
        with patch('collector.tls_context',return_value=None),patch('collector.time.monotonic',return_value=1),patch('collector.get_json',side_effect=responses) as fetch,patch.object(engine,'update_ticker',side_effect=store_and_stop):
            manager=Collector(engine)
            manager.ticker_loop('bithumb')
        self.assertEqual(fetch.call_count,2)
        self.assertEqual(engine.tickers['bithumb'].markets,{'BTC':'KRW-BTC'})
        self.assertEqual(engine.tickers['bithumb'].amounts,{'BTC':D(10)})


class Http(unittest.TestCase):
    def test_api_mime_hosts_and_traversal(self):
        with tempfile.TemporaryDirectory() as root:
            root=Path(root);web=root/'web';web.mkdir()
            (web/'index.html').write_text('<script src="app.js"></script>')
            (web/'app.js').write_text('export {};')
            (root/'private.txt').write_text('PRIVATE')
            engine=Engine(lambda:NOW)
            server=Server(('127.0.0.1',0),engine,web)
            thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
            try:
                def get(path,host=None):
                    connection=http.client.HTTPConnection('127.0.0.1',server.server_port,timeout=2)
                    connection.request('GET',path,headers={'Host':host or f'127.0.0.1:{server.server_port}'})
                    response=connection.getresponse();body=response.read();headers=dict(response.getheaders());status=response.status;connection.close()
                    return status,headers,body
                self.assertEqual(get('/api/portable/status')[0],200)
                code,headers,body=get('/app.js');self.assertEqual(code,200);self.assertIn('text/javascript',headers['Content-type'])
                self.assertEqual(get('/../private.txt')[0],404)
                self.assertEqual(get('/%2e%2e/private.txt')[0],404)
                self.assertEqual(get('/api/portable/status','evil.example')[0],403)
                self.assertEqual(get('/api/portable/state?window=11')[0],400)
                self.assertEqual(get('/api/market-share/index-day?day=2020-01-01')[0],400)
                code,_,body=get('/api/market-share/index-day?projection=domestic_grid')
                self.assertEqual(json.loads(body)['contract_version'],'INDEX_DAY_GRID_V1')
            finally:server.shutdown();server.server_close();thread.join(2)


if __name__=='__main__':unittest.main(verbosity=2)
