# 포터블 공지 타임라인과 관련 거래량

Windows·Apple Silicon Mac 공통 Python 서버 3.3.0과 동일한 React 화면을 사용합니다.
대시보드 상단의 **공지 타임라인**으로 `/notices/`를 열고, **시장 인덱스**로 돌아옵니다.
공지 본문·필터·일정 타임라인과 코인 선택 기능은 기존 `NoticePortal`을 사용합니다.

## 자료 경계

- 공지 목록과 분석된 일정: `https://notice.realmi.net`의 공개 읽기 API. 로컬 AI나 별도 크롤러를 실행하지 않습니다.
- 공지 목록: `/api/market-share/notices`의 페이지·version·as_of_ms를 그대로 전달합니다. 409이면 브라우저가 한 번 새 스냅샷으로 다시 읽습니다.
- 타임라인: `/api/market-share/notice-portal`의 `rolling_24h`만 읽습니다. 원격 시장 거래량은 포터블 계산에 섞지 않습니다.
- 최근 10분 거래량: 이 PC가 수신한 국내 5곳의 원화 체결입니다. 마지막으로 끝난 초까지 금액·체결 수·기초자산 수량을 합산합니다. 중복 체결을 제외하고 연결 결측을 유지합니다.
- 시간봉 거래금액: 이 PC의 `data/index-hours.sqlite3`에 저장한 공식 원화 마켓 시간봉입니다. 부분 수집은 부분 자료이며 누락값은 0으로 채우지 않습니다.
- 거래 수량이 없는 자료는 `null`로 유지합니다. 다른 거래소·다른 코인의 수량이나 거래금액을 대입하지 않습니다.

공지 요청은 고정된 HTTPS 호스트의 두 읽기 경로만 사용합니다. 페이지 크기는 최대 100,
성공 응답은 60초·최대 32키 캐시이며 체결값은 매 응답 시 로컬에서 다시 계산합니다.
공지 서버 장애는 오류로 표시합니다. LLM 질문·작성 API는 제공하지 않습니다.
사용자의 PC나 운영 서버에서 과거 누락 시세 백필·자동 복구를 시작하지 않습니다.

## 확인

```sh
PYTHONPATH=source/scripts/portable-python source/.venv/bin/python -m unittest discover -s source/tests/portable_python -q
cd source/frontend
pnpm check
pnpm exec node --test client/src/lib/notice-portal.test.ts client/src/lib/notice-archive.test.ts
```

배포 전 통합 ZIP의 두 플랫폼에서 `app/`·`web/` 바이트가 같은지 검사하고,
Mac 패키지로 공지 진입·목록·타임라인·코인 선택·관련 거래량·대시보드 복귀를 확인합니다.
실제 Windows 실행과 장시간 수집 검증 여부는 배포 결과에 별도로 기록합니다.
