"""Thread-safe accounting and immutable HTTP projections for the portable app."""
from __future__ import annotations

import copy
import math
import threading
import time
from collections import OrderedDict
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from decimal import Decimal
from typing import Callable

from adapters import VENUES, Trade

WINDOWS = (10, 30, 60, 300, 600, 1800, 3600, 7200, 14400, 21600, 43200, 86400)
KST = timezone(timedelta(hours=9))
ZERO = Decimal(0)


def day_at(now: float) -> str:
    return datetime.fromtimestamp(now, KST).strftime('%Y-%m-%d')


def cell() -> dict:
    return dict(total=None, krw=None, btc=None, usdt=None, share=None, partial=False, unavailable=False)


def add_cell(target: dict, amount: Decimal | None, partial: bool) -> None:
    if amount is not None:
        target['total'] = (target['total'] or ZERO) + amount
        target['krw'] = target['total']
    target['partial'] |= partial or amount is None
    target['unavailable'] |= amount is None


def finish_cells(cells: dict, denominator: Decimal | None, total: bool = False) -> None:
    domestic, combined = cell(), cell()
    for venue in VENUES:
        if venue in cells:
            value = cells[venue]
            add_cell(domestic, value['total'], value['partial'])
            if venue in ('bithumb', 'upbit'):
                add_cell(combined, value['total'], value['partial'])
    cells.update(domestic=domestic, combined=combined)
    for venue in (*VENUES, 'combined'):
        if venue in cells and cells[venue]['total'] is not None and domestic['total']:
            cells[venue]['share'] = cells[venue]['total'] * 100 / domestic['total']
    if total:
        denominator = domestic['total']
    if denominator and domestic['total'] is not None:
        domestic['share'] = domestic['total'] * 100 / denominator


def grid(rows: list[dict], query: dict) -> dict:
    selected = set(query.get('assets', '').split(',')) if 'assets' in query else None
    totals, coins = {}, {}
    for row in rows:
        base, venue = row['base'], row['exchange']
        if selected is not None and base not in selected:
            continue
        if query.get('quote', 'all').lower() not in ('all', 'krw'):
            continue
        chosen = query.get('venue', 'all')
        if chosen not in ('all', venue) and not (chosen == 'combined' and venue in ('bithumb', 'upbit')):
            continue
        own = coins.setdefault(base, {})
        add_cell(own.setdefault(venue, cell()), row['volume_krw'], row['status'] != 'COMPLETE')
        add_cell(totals.setdefault(venue, cell()), row['volume_krw'], row['status'] != 'COMPLETE')
    finish_cells(totals, None, True)
    result = []
    for base, cells in coins.items():
        finish_cells(cells, totals['domestic']['total'])
        result.append({'base': base, 'cells': cells})
    result.sort(key=lambda c: (c['cells']['domestic']['total'] is None,
                              -(c['cells']['domestic']['total'] or ZERO), c['base']))
    return {'totals': totals, 'coins': result}


def row(venue: str, base: str, amount: Decimal | None, partial: bool, source: str) -> dict:
    return dict(exchange=venue, base=base, market=f'KRW-{base}', quote='KRW', volume_krw=amount,
                quote_volume=amount, base_volume=None, hours=0, amount_hours=0,
                status='PARTIAL' if partial else 'COMPLETE', sources=[source])


@dataclass
class Snapshot:
    markets: dict[str, str] = field(default_factory=dict)
    amounts: dict[str, Decimal] = field(default_factory=dict)
    at: float = 0
    error: str = ''


@dataclass
class Feed:
    venue: str
    state: str = 'CONNECTING'
    since: float = 0
    at: float = 0
    error: str = ''
    connected_at: float = 0


class Engine:
    def __init__(self, clock: Callable[[], float] = time.time, max_records: int = 250_000, clock_info=None, archive=None, clock_sync=None):
        self.clock = clock
        self.clock_info = clock_info
        self.clock_sync = clock_sync
        self.archive = archive
        self.lock = threading.RLock()
        self.stop = threading.Event()
        self.tickers = {v: Snapshot() for v in VENUES}
        self.feeds: dict[str, Feed] = {}
        self.expected = {v: set() for v in VENUES}
        self.buckets: dict[str, dict[int, dict[str, tuple[Decimal, int]]]] = {v: {} for v in VENUES}
        self.seen: OrderedDict[tuple[str, str, str], float] = OrderedDict()
        self.oldest = int(clock())
        self.records = 0
        self.max_records = max_records
        self.version = 0
        self.cache: dict[tuple[int, int], dict] = {}
        self.cache_version = -1
        self.last_prune = clock()

    def update_ticker(self, venue: str, markets: dict, amounts: dict, error: str = '') -> None:
        with self.lock:
            old = self.tickers[venue]
            self.tickers[venue] = Snapshot(dict(markets), dict(amounts), self.clock(), error) if not error else Snapshot(
                old.markets, old.amounts, old.at, error[:500])
            self.version += 1

    def set_expected(self, venue: str, keys: set[str]) -> None:
        with self.lock:
            for key in self.expected[venue] - keys:
                self.feeds.pop(key, None)
            self.expected[venue] = set(keys)
            for key in keys:
                self.feeds.setdefault(key, Feed(venue, at=self.clock()))
            self.version += 1

    def feed_status(self, key: str, venue: str, state: str, error: str = '') -> None:
        with self.lock:
            now = self.clock()
            old = self.feeds.get(key, Feed(venue))
            since = now if state == 'WAITING' and old.state != 'WAITING' else old.since
            if state == 'CONNECTING':
                since = 0
            connected = now if state == 'WAITING' and old.state != 'WAITING' else old.connected_at
            self.feeds[key] = Feed(venue, state, since, now, error[:500], connected)
            self.version += 1

    def subscription_ready(self, key: str) -> None:
        with self.lock:
            if key in self.feeds:
                self.feeds[key].since = self.clock()
                self.version += 1

    def heartbeat(self, key: str) -> None:
        with self.lock:
            if key in self.feeds:
                self.feeds[key].at = self.clock()

    def accept(self, key: str, venue: str, trade: Trade, allowed: set[str]) -> bool:
        with self.lock:
            now = self.clock()
            feed = self.feeds.get(key)
            if not feed or feed.state not in ('WAITING', 'LIVE') or trade.base not in allowed:
                return False
            if not math.isfinite(trade.at) or trade.at < feed.connected_at - 2 or trade.at > now + 5:
                return False
            if not trade.amount.is_finite() or trade.amount <= 0 or trade.amount > Decimal('1e30'):
                return False
            identity = (venue, trade.base, trade.identity)
            if identity in self.seen:
                return False
            self.seen[identity] = now
            second = int(trade.at)
            if second < self.oldest:
                # Never reinsert records that have already expired from retention.
                return False
            bucket = self.buckets[venue].setdefault(second, {})
            previous, count = bucket.get(trade.base, (ZERO, 0))
            if trade.base not in bucket:
                self.records += 1
            bucket[trade.base] = (previous + trade.amount, count + 1)
            feed.state, feed.at, feed.error = 'LIVE', now, ''
            self.version += 1
            self._prune(now)
            return True

    def _prune(self, now: float) -> None:
        if now - self.last_prune < 5 and self.records <= self.max_records and len(self.seen) <= 200_000:
            return
        cutoff = int(now) - 86400
        while self.oldest < cutoff or self.records > self.max_records:
            for venue in VENUES:
                self.records -= len(self.buckets[venue].pop(self.oldest, {}))
            self.oldest += 1
        while self.seen and (next(iter(self.seen.values())) < now - 180 or len(self.seen) > 200_000):
            self.seen.popitem(last=False)
        self.last_prune = now

    def _live(self, seconds: int, end: int) -> dict:
        if self.cache_version != self.version:
            self.cache.clear()
            self.cache_version = self.version
        cache_key = (seconds, end)
        if cache_key in self.cache:
            return self.cache[cache_key]
        if len(self.cache) > 40:
            self.cache.clear()
        rows, venues = [], []
        total, trade_total, coverage, complete = ZERO, 0, seconds, True
        for venue in VENUES:
            feeds = [self.feeds[k] for k in self.expected[venue] if k in self.feeds]
            ready = bool(feeds) and len(feeds) == len(self.expected[venue]) and all(
                f.state == 'LIVE' and f.at >= end - 90 for f in feeds)
            first = max([self.oldest, *(int(f.since) + 1 for f in feeds)]) if feeds else end + 1
            covered = max(0, min(seconds, end - first + 1)) if ready else 0
            partial = not ready or covered < seconds
            complete &= not partial
            coverage = min(coverage, covered)
            assets = {}
            for second in range(max(end - seconds + 1, self.oldest), end + 1):
                for base, (amount, count) in self.buckets[venue].get(second, {}).items():
                    old, old_count = assets.get(base, (ZERO, 0))
                    assets[base] = (old + amount, old_count + count)
            amount = sum((a for a, _ in assets.values()), ZERO)
            count = sum(c for _, c in assets.values())
            reported = amount if count or not partial else None
            if reported is not None:
                total += reported
                trade_total += count
            for base, (value, _) in assets.items():
                rows.append(row(venue, base, value, partial, 'OBSERVED_KRW_TRADES'))
            venues.append(dict(exchange=venue, volume_krw=reported, trade_count=count if reported is not None else None,
                               share_pct=None, trade_count_share_pct=None, partial=partial))
        for venue in venues:
            if venue['volume_krw'] is not None and total:
                venue['share_pct'] = venue['volume_krw'] * 100 / total
            if venue['trade_count'] is not None and trade_total:
                venue['trade_count_share_pct'] = Decimal(venue['trade_count']) * 100 / trade_total
        value = dict(rows=rows, exchanges=venues, total=total, coverage=coverage, complete=complete)
        self.cache[cache_key] = value
        return value

    def ticker_index(self, query: dict) -> dict:
        with self.lock:
            now = self.clock()
            if query.get('day', day_at(now)) != day_at(now):
                raise ValueError('This Python package has no archived hourly candles. Select today.')
            rows, partial = [], False
            oldest = now
            for venue in VENUES:
                snap = self.tickers[venue]
                valid = bool(snap.markets) and not snap.error and 0 <= now - snap.at <= 30
                partial |= not valid
                if snap.at:
                    oldest = min(oldest, snap.at)
                for base in snap.markets:
                    amount = snap.amounts.get(base) if valid else None
                    rows.append(row(venue, base, amount, not valid, 'OFFICIAL_KRW_24H_TICKER'))
            domestic = grid(rows, query)
            if partial:
                for cells in [domestic['totals'], *(c['cells'] for c in domestic['coins'])]:
                    for value in cells.values():
                        value['partial'] = True
            return dict(contract_version='INDEX_TICKER_V1', storage_mode='TICKER_24H', day=day_at(now),
                        start=int(oldest) - 86400, end=int(oldest), status='PARTIAL' if partial else 'COMPLETE',
                        membership='CURRENT_KRW_MARKETS', usdt_close_krw=None, rows=rows, exchanges=[], domestic=domestic)

    def index_day(self, query: dict) -> dict:
        if query.get('source') == 'ticker_24h' or self.archive is None:
            return self.ticker_index(query)
        now = self.clock()
        day = query.get('day', day_at(now))
        try:
            date = datetime.strptime(day, '%Y-%m-%d').replace(tzinfo=KST)
        except ValueError as exc:
            raise ValueError('day must use YYYY-MM-DD') from exc
        if date.strftime('%Y-%m-%d') != day or day > day_at(now):
            raise ValueError('Invalid or future index date')
        if day == day_at(now):
            end = int(now) // 3600 * 3600
            start = end - 86400
        else:
            start, end = int(date.timestamp()), int((date + timedelta(days=1)).timestamp())
        values = self.archive.snapshot(start, end)
        by_market = {}
        for value in values:
            by_market.setdefault((value['venue'], value['base'], value['market']), []).append(value)
        rows = []
        for (venue, base, market), hours in sorted(by_market.items()):
            valid = [h for h in hours if h['status'] == 'COMPLETE']
            amount = sum((Decimal(h['amount']) for h in valid), ZERO) if valid else None
            partial = len(valid) < 24
            item = row(venue, base, amount, partial, 'OFFICIAL_KRW_HOURLY_CANDLE')
            item.update(market=market, hours=len(valid), amount_hours=len(valid),
                        base_volume=sum((Decimal(h['base_volume']) for h in valid), ZERO) if valid else None,
                        status='TURNOVER_UNAVAILABLE' if venue in ('korbit', 'gopax') else
                        'ROLLING_COLLECTING' if any(h['status'] == 'COLLECTING' for h in hours) else
                        'INCOMPLETE_HOURS' if partial else 'COMPLETE')
            rows.append(item)
        domestic = grid(rows, query)
        for venue in VENUES:
            domestic['totals'].setdefault(venue, dict(cell(), partial=True, unavailable=True))
        # A missing entire venue is not a complete domestic denominator.
        partial = not rows or any(r['status'] != 'COMPLETE' for r in rows) or any(
            domestic['totals'][v]['total'] is None for v in VENUES)
        if partial:
            for cells in [domestic['totals'], *(c['cells'] for c in domestic['coins'])]:
                for value in cells.values():
                    value['partial'] = True
        exchanges = [dict(exchange=v, volume_krw=domestic['totals'][v]['total'],
                          share_pct=domestic['totals'][v]['share'],
                          complete_markets=sum(r['exchange'] == v and r['status'] == 'COMPLETE' for r in rows),
                          market_count=sum(r['exchange'] == v for r in rows), status='PARTIAL' if partial else 'COMPLETE')
                     for v in VENUES]
        return dict(contract_version='INDEX_DAY_HOURLY_V1', storage_mode='ROLLING_24H_LATEST' if day == day_at(now) else 'HOURLY_ARCHIVE',
                    day=day, start=start, end=end, status='PARTIAL' if partial else 'COMPLETE',
                    membership='COLLECTED_KRW_MARKETS', usdt_close_krw=None, rows=rows,
                    exchanges=exchanges, domestic=domestic)

    @staticmethod
    def window(query: dict) -> int:
        seconds = int(query.get('window', 10))
        if seconds not in WINDOWS:
            raise ValueError('Unsupported window')
        return seconds

    def realtime_index(self, query: dict) -> dict:
        with self.lock:
            seconds = self.window(query)
            if seconds == 86400:
                ticker = self.ticker_index(query)
                complete = ticker['status'] == 'COMPLETE'
                domestic, end = ticker['domestic'], ticker['end']
                source = 'OFFICIAL_KRW_24H_TICKER'
                coverage = 86400 if complete else 0
            else:
                end = int(self.clock()) - 1
                live = self._live(seconds, end)
                complete, coverage = live['complete'], live['coverage']
                domestic, source = grid(live['rows'], query), 'OBSERVED_KRW_TRADES'
            return dict(contract_version='REALTIME_INDEX_V1', storage_mode='REALTIME_WINDOW',
                        day=day_at(self.clock()), start=end - seconds + 1, end=end + 1, as_of_second=end,
                        window_sec=seconds, coverage_sec=coverage, window_complete=complete,
                        status='COMPLETE' if complete else 'PARTIAL', membership=source, data_source=source,
                        usdt_close_krw=None, rows=[], exchanges=[], domestic=domestic)

    def state(self, query: dict) -> dict:
        with self.lock:
            seconds = self.window(query)
            end = int(self.clock()) - 1
            if seconds == 86400:
                ticker = self.ticker_index({})
                current = [dict(exchange=v, volume_krw=ticker['domestic']['totals'].get(v, cell())['total'],
                                share_pct=ticker['domestic']['totals'].get(v, cell())['share'], trade_count=None,
                                trade_count_share_pct=None, partial=ticker['status'] != 'COMPLETE') for v in VENUES]
                point = {'at': ticker['end'] * 1000, **{f'share_{r["exchange"]}': r['share_pct'] for r in current}}
                changes = [dict(exchange=r['exchange'], amount=r['volume_krw'], after=r['share_pct'],
                                before=None, delta=None, share_delta=None) for r in current]
                partial = ticker['status'] != 'COMPLETE'
            else:
                live = self._live(seconds, end)
                previous = {r['exchange']: r for r in self._live(seconds, end - seconds)['exchanges']}
                current, partial = copy.deepcopy(live['exchanges']), not live['complete']
                point = {'at': (end + 1) * 1000, **{f'share_{r["exchange"]}': r['share_pct'] for r in current}}
                changes = []
                for r in current:
                    old = previous[r['exchange']]
                    changes.append(dict(exchange=r['exchange'], amount=r['volume_krw'], before=old['share_pct'],
                                        after=r['share_pct'], delta=(r['volume_krw'] - old['volume_krw'])
                                        if r['volume_krw'] is not None and old['volume_krw'] is not None else None,
                                        share_delta=(r['share_pct'] - old['share_pct'])
                                        if r['share_pct'] is not None and old['share_pct'] is not None else None))
            if partial:
                point['partial'] = 1
            strip = []
            for window in (10, 30, 60, 600, 1800, 3600):
                live = self._live(window, end)
                bithumb = next(r for r in live['exchanges'] if r['exchange'] == 'bithumb')
                strip.append(dict(window=window, share=bithumb['share_pct'], partial=not live['complete']))
            connections = []
            for venue in VENUES:
                snapshot = self.tickers[venue]
                connections.append(dict(exchange=venue, ticker_at=snapshot.at, ticker_error=snapshot.error,
                                        feeds=[vars(copy.copy(self.feeds[k])) for k in self.expected[venue] if k in self.feeds]))
            return dict(at=end, window=seconds, partial=partial, exchanges=current, changes=changes,
                        strip=strip, point=point, connections=connections,
                        hourly=self.archive.info() if self.archive else None,
                        clock=self.clock_info() if self.clock_info else None)

    def filters(self) -> dict:
        archived = {}
        if self.archive:
            with self.archive.lock:
                archived = dict(self.archive.markets)
        with self.lock:
            listings = {}
            for venue, base in archived:
                listings.setdefault(base, set()).add(venue)
            for venue in VENUES:
                for base in self.tickers[venue].markets:
                    listings.setdefault(base, set()).add(venue)
            assets = [dict(asset=base, listed_on_bithumb='bithumb' in venues,
                           exclusive_on_bithumb=venues == {'bithumb'},
                           shared_bithumb_upbit={'bithumb', 'upbit'} <= venues) for base, venues in listings.items()]
            return dict(contract_version='INDEX_FILTERS_V1', assets=assets, notice_assets=[])
