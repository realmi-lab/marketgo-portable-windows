"""Official KRW-only exchange adapters. No estimated candle turnover."""
from __future__ import annotations

from dataclasses import dataclass
from decimal import Decimal, InvalidOperation
from typing import Any

VENUES = ('bithumb', 'upbit', 'coinone', 'korbit', 'gopax')
TICKERS = {
    'bithumb': 'https://api.bithumb.com/public/ticker/ALL_KRW',
    'upbit': 'https://api.upbit.com/v1/ticker/all?quote_currencies=KRW',
    'coinone': 'https://api.coinone.co.kr/public/v2/ticker_new/KRW',
    'korbit': 'https://api.digitalx.miraeasset.com/v2/tickers',
    'gopax': 'https://api.gopax.co.kr/tickers',
}
REGISTRIES = {
    'bithumb': 'https://api.bithumb.com/v1/market/all?isDetails=true',
    'upbit': 'https://api.upbit.com/v1/market/all?is_details=true',
    'coinone': 'https://api.coinone.co.kr/public/v2/markets/KRW',
    'korbit': 'https://api.digitalx.miraeasset.com/v2/currencyPairs',
    'gopax': 'https://api.gopax.co.kr/trading-pairs',
}
SOCKETS = {
    'bithumb': 'wss://ws-api.bithumb.com/websocket/v1',
    'upbit': 'wss://api.upbit.com/websocket/v1',
    'coinone': 'wss://stream.coinone.co.kr',
    'korbit': 'wss://ws-api.digitalx.miraeasset.com/v2/public',
    'gopax': 'wss://wsapi.gopax.co.kr',
}
CHUNK_SIZES = {'bithumb': 200, 'upbit': 200, 'coinone': 100, 'korbit': 200, 'gopax': 50}


def number(value: Any) -> Decimal:
    if value is None or isinstance(value, bool):
        raise ValueError('Missing or invalid numeric value')
    try:
        result = Decimal(str(value))
    except InvalidOperation as exc:
        raise ValueError('Invalid numeric value') from exc
    if not result.is_finite() or result < 0 or result > Decimal('1e30'):
        raise ValueError('Non-finite, negative, or out-of-range numeric value')
    return result


def timestamp(value: Any) -> float:
    if isinstance(value, str) and ('T' in value or '-' in value):
        from datetime import datetime
        return datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()
    result = float(number(value))
    if result > 1e13:
        result /= 1000
    if result > 1e11:
        result /= 1000
    return result


def registry(venue: str, payload: Any) -> dict[str, str]:
    out = {}
    if venue in ('bithumb', 'upbit'):
        out = {r['market'][4:].upper(): r['market'] for r in payload if r['market'].startswith('KRW-')}
    elif venue == 'coinone':
        out = {r['target_currency'].upper(): 'KRW-' + r['target_currency'].upper()
               for r in payload['markets'] if int(r['trade_status']) in (1, 2, 3)}
    elif venue == 'korbit':
        if payload.get('success') is not True:
            raise ValueError('DigitalX registry rejected')
        out = {r['symbol'][:-4].upper(): r['symbol'] for r in payload['data']
               if r['status'] == 'launched' and r['symbol'].endswith('_krw')}
    elif venue == 'gopax':
        out = {r['baseAsset'].upper(): r['name'] for r in payload if r['quoteAsset'] == 'KRW'}
    if not out:
        raise ValueError(f'{venue}: empty active KRW registry')
    return out


def tickers(venue: str, payload: Any, markets: dict[str, str]) -> dict[str, Decimal]:
    if not markets:
        raise ValueError(f'{venue}: active KRW registry is not initialized')
    if venue == 'bithumb':
        if payload.get('status') != '0000':
            raise ValueError('Bithumb ticker rejected')
        pairs = [(base, row['acc_trade_value_24H']) for base, row in payload['data'].items() if base != 'date']
    elif venue == 'upbit':
        pairs = [(r['market'][4:], r['acc_trade_price_24h']) for r in payload if r['market'].startswith('KRW-')]
    elif venue == 'coinone':
        if payload.get('result') != 'success':
            raise ValueError('Coinone ticker rejected')
        pairs = [(r['target_currency'], r['quote_volume']) for r in payload['tickers'] if r['quote_currency'].upper() == 'KRW']
    elif venue == 'korbit':
        if payload.get('success') is not True:
            raise ValueError('DigitalX ticker rejected')
        pairs = [(r['symbol'][:-4], r['quoteVolume']) for r in payload['data'] if r['symbol'].endswith('_krw')]
    else:
        pairs = [(r['tradingPairName'][:-4], r['quoteVolume']) for r in payload if r['tradingPairName'].endswith('-KRW')]
    out = {}
    for base, value in pairs:
        base = base.upper()
        if base not in markets:
            continue
        if base in out:
            raise ValueError(f'{venue}: duplicate ticker market {base}')
        out[base] = number(value)
    if set(out) != set(markets):
        raise ValueError(f'{venue}: ticker market coverage mismatch {len(out)}/{len(markets)}')
    return out


def ensure_fresh(venue: str, payload: Any, markets: dict[str, str], now: float) -> None:
    if venue == 'bithumb':
        values = [payload['data']['date']]
    elif venue == 'coinone':
        values = [payload['server_time']]
    else:
        # These APIs expose last execution times, not response times. A quiet
        # market is still a valid rolling ticker. HTTP/cache and local receipt
        # freshness are checked independently by the collector and engine.
        return
    newest = max((timestamp(v) for v in values if v not in (None, 0, '0', '')), default=0)
    if newest < now - 120 or newest > now + 30:
        raise ValueError(f'{venue}: official ticker time differs from collection UTC by {newest-now:+.1f}s')


def subscriptions(venue: str, markets: dict[str, str], ticket: str) -> list[Any]:
    symbols = list(markets.values())
    if venue in ('bithumb', 'upbit'):
        field = 'isOnlyRealtime' if venue == 'bithumb' else 'is_only_realtime'
        return [[{'ticket': ticket}, {'type': 'trade', 'codes': symbols, field: True}, {'format': 'DEFAULT'}]]
    if venue == 'coinone':
        return [{'request_type': 'SUBSCRIBE', 'channel': 'TRADE', 'topic': {'quote_currency': 'KRW', 'target_currency': base}}
                for base in markets]
    if venue == 'korbit':
        return [[{'method': 'subscribe', 'type': 'trade', 'symbols': symbols}]]
    return [{'i': index + 1, 'n': 'SubscribeToTradingPair', 'o': {'tradingPairName': symbol, 'limit': 20}}
            for index, symbol in enumerate(symbols)]


@dataclass(frozen=True)
class Trade:
    base: str
    identity: str
    at: float
    amount: Decimal


def trades(venue: str, payload: dict) -> list[Trade]:
    if payload.get('error') or payload.get('response_type', payload.get('r')) == 'ERROR':
        raise ValueError(f'{venue}: subscription rejected: {str(payload)[:400]}')
    rows = []
    if venue in ('bithumb', 'upbit') and payload.get('type') == 'trade':
        if not payload['code'].startswith('KRW-') or payload.get('stream_type') == 'SNAPSHOT':
            return []
        rows = [(payload['code'][4:], payload['sequential_id'], payload['trade_timestamp'], payload['trade_price'], payload['trade_volume'])]
    elif venue == 'coinone' and payload.get('channel', payload.get('c')) == 'TRADE' and payload.get('response_type', payload.get('r')) == 'DATA':
        row = payload.get('data', payload.get('d'))
        if row.get('quote_currency', row.get('qc')).upper() != 'KRW':
            return []
        rows = [(row.get('target_currency', row.get('tc')), row.get('id', row.get('i')),
                 row.get('timestamp', row.get('t')), row.get('price', row.get('p')), row.get('qty', row.get('q')))]
    elif venue == 'korbit' and payload.get('type') == 'trade' and not payload.get('snapshot'):
        if not payload['symbol'].endswith('_krw'):
            return []
        rows = [(payload['symbol'][:-4], r['tradeId'], r['timestamp'], r['price'], r['qty']) for r in payload['data']]
    elif venue == 'gopax' and payload.get('n') == 'PublicTradeEvent':
        row = payload['o']
        if not row['tradingPairName'].endswith('-KRW'):
            return []
        rows = [(row['tradingPairName'][:-4], row['tradeId'], row['occurredAt'], row['price'], row['baseAmount'])]
    out = []
    for base, identity, at, price, quantity in rows:
        if identity is None or str(identity) == '':
            raise ValueError('Trade has no replay identity')
        amount = number(price) * number(quantity)
        if amount > 0:
            out.append(Trade(base.upper(), str(identity), timestamp(at), amount))
    return out
