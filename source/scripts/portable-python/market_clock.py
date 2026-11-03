"""UTC reference calibrated from multiple verified HTTPS authorities.

Only the process clock is corrected. The OS clock and TLS verification are
never changed; monotonic elapsed time protects collection from wall-clock edits.
"""
from __future__ import annotations

from dataclasses import dataclass
from email.utils import parsedate_to_datetime
import logging
import queue
import statistics
import threading
import time
import urllib.error
import urllib.request
from urllib.parse import urlsplit

LOG = logging.getLogger('marketgo')
SOURCES = {
    'bithumb': 'https://api.bithumb.com/v1/market/all?isDetails=true',
    'upbit': 'https://api.upbit.com/v1/market/all?is_details=true',
    'coinone': 'https://api.coinone.co.kr/public/v2/markets/KRW',
    'digitalx': 'https://api.digitalx.miraeasset.com/v2/currencyPairs',
}


@dataclass(frozen=True)
class Sample:
    authority: str
    utc_at_receipt: float
    received_mono: float


def https_sample(authority, url, context, monotonic=time.monotonic):
    before = monotonic()
    request = urllib.request.Request(url, method='HEAD', headers={
        'User-Agent': 'MarketGO-Portable-Python/3.1', 'Cache-Control': 'no-cache'})
    try:
        response = urllib.request.urlopen(request, context=context, timeout=4)
    except urllib.error.HTTPError as error:
        # A verified HTTPS 404/405 still carries the authority's HTTP clock.
        response = error
    try:
        after = monotonic()
        target = urlsplit(response.geturl())
        source = urlsplit(url)
        if target.scheme != 'https' or target.hostname != source.hostname:
            raise ValueError('Unexpected clock-source redirect')
        if not 0 <= after - before <= 4:
            raise ValueError('Clock reference round trip exceeds four seconds')
        date = response.headers.get('Date')
        if not date:
            raise ValueError('Clock source has no HTTP Date')
        parsed = parsedate_to_datetime(date)
        if parsed.tzinfo is None:
            raise ValueError('Clock source has no UTC offset')
        age = int(response.headers.get('Age', '0'))
        if not 0 <= age <= 120:
            raise ValueError('Clock source is cached too long')
        # HTTP Date is second-resolution. The half-second term accounts for
        # truncation and RTT/2 estimates the elapsed one-way response path.
        remote = parsed.timestamp() + age + 0.5 + (after - before) / 2
        return Sample(authority, remote, after)
    finally:
        response.close()


class MarketClock:
    def __init__(self, wall=time.time, monotonic=time.monotonic):
        self.wall, self.monotonic = wall, monotonic
        self._utc = wall()
        self._mono = monotonic()
        self._lock = threading.RLock()
        self._mode = 'local'
        self._sources = []
        self._error = 'Server clock has not been verified'

    def time(self):
        with self._lock:
            return self._utc + self.monotonic() - self._mono

    def synchronize(self, context, fetch=https_sample):
        samples, errors = [], []
        results = queue.Queue()
        def request(name, url):
            try:
                results.put((fetch(name, url, context, self.monotonic), None))
            except Exception as error:
                results.put((None, f'{name}: {error}'))
        # DNS lookup isn't reliably covered by socket timeouts. Daemon workers
        # and a separate deadline keep an offline PC from hanging startup.
        for name, url in SOURCES.items():
            threading.Thread(target=request, args=(name, url), name=f'clock-{name}', daemon=True).start()
        deadline = time.monotonic() + 5
        for _ in SOURCES:
            try:
                sample, error = results.get(timeout=max(0, deadline - time.monotonic()))
            except queue.Empty:
                errors.append('Clock lookup deadline exceeded')
                break
            if sample:
                samples.append(sample)
            if error:
                errors.append(error)
        reference = self.monotonic()
        values = sorted((s.utc_at_receipt + reference - s.received_mono, s.authority) for s in samples)
        clusters = [[v for v in values if start[0] <= v[0] <= start[0] + 5] for start in values]
        cluster = max(clusters, key=len, default=[])
        if len({name for _, name in cluster}) < 2:
            with self._lock:
                self._error = 'No agreement from two HTTPS clocks. Check Windows time synchronization and restart. ' + '; '.join(errors)[:300]
            LOG.warning('Clock reference unavailable: %s', self._error)
            return False
        with self._lock:
            self._utc = statistics.median(v for v, _ in cluster)
            self._mono = reference
            self._sources = [name for _, name in cluster]
            self._mode = 'server'
            self._error = ''
        LOG.info('Collection UTC calibrated; PC offset=%+.3fs; sources=%s',
                 self.time() - self.wall(), ','.join(self._sources))
        return True

    def info(self):
        with self._lock:
            return {'mode': self._mode, 'offset_seconds': round(self.time() - self.wall(), 3),
                    'sources': list(self._sources), 'error': self._error}
