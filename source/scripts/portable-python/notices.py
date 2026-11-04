"""Read-only bridge to the existing public notice archive and schedule feed."""
from __future__ import annotations

import copy
import threading
import time
from collections import OrderedDict
from urllib.error import HTTPError
from urllib.parse import urlencode

ORIGIN = 'https://notice.realmi.net'


class NoticeUnavailable(Exception):
    def __init__(self, status=502):
        self.status = status
        super().__init__('공지 서버에 연결하지 못했습니다. 잠시 후 다시 확인해 주세요.')


class NoticeFeed:
    def __init__(self, fetch, clock=time.monotonic):
        self.fetch, self.clock = fetch, clock
        self.lock = threading.Lock()
        self.cache = OrderedDict()

    def _read(self, path):
        with self.lock:
            cached = self.cache.get(path)
            if cached and cached[0] > self.clock():
                return copy.deepcopy(cached[1])
        try:
            value = self.fetch(ORIGIN + path)
        except HTTPError as exc:
            # Preserve version conflicts so the browser restarts its paged read.
            if exc.code == 409:
                with self.lock:
                    self.cache.clear()
            exc.close()
            raise NoticeUnavailable(exc.code if exc.code in (409, 429) else 502) from exc
        except Exception as exc:
            raise NoticeUnavailable() from exc
        with self.lock:
            self.cache[path] = (self.clock() + 60, value)
            self.cache.move_to_end(path)
            while len(self.cache) > 32:
                self.cache.popitem(last=False)
        return copy.deepcopy(value)

    def archive(self, query):
        params = {}
        for name, default in (('offset', 0), ('limit', 20), ('version', None), ('as_of_ms', None)):
            raw = query.get(name, default)
            if raw is None:
                continue
            value = int(raw)
            if value < 0 or (name == 'limit' and not 1 <= value <= 100):
                raise ValueError('Invalid notice pagination')
            params[name] = value
        if 'fields' in query:
            if query['fields'] != 'headline':
                raise ValueError('Unsupported notice fields')
            params['fields'] = 'headline'
        value = self._read('/api/market-share/notices?' + urlencode(params))
        if not isinstance(value, dict) or not isinstance(value.get('snapshot', {}).get('items'), list):
            raise NoticeUnavailable()
        return value

    def timeline(self):
        value = self._read('/api/market-share/notice-portal')
        rolling = value.get('rolling_24h', {}) if isinstance(value, dict) else {}
        if not isinstance(rolling, dict) or not isinstance(rolling.get('evidence', {}).get('domestic_notices'), list):
            raise NoticeUnavailable()
        # Upstream market amounts are deliberately excluded: amounts are local.
        return rolling
