# 공통 포터블 시간봉 인덱스

Windows와 Apple Silicon Mac은 같은 `scripts/portable-python/`와 동일한 React 빌드를 사용한다.
플랫폼별 Python 3.14.10 런타임·실행기만 다르다. 통합 ZIP 빌드는 두 플랫폼의 `app/`와 `web/` 파일을 바이트 단위로 대조한다.

## 수집과 저장

- 시작 시 가장 최근에 끝난 UTC 1시간 구간만 수집한다. 해당 구간 종료 후 2분이 지나야 요청한다.
- 다음 정시 2분 뒤 새 구간을 수집한다. PC 종료·잠자기 동안의 구간을 순서대로 따라가지 않는다.
- 공식 원화 거래금액을 제공하는 빗썸·업비트·코인원에서 시장당 한 번 요청한다. 거래소별 단일 워커, 요청 사이 최소 0.25초, HTTP 제한 8초다.
- 요청의 count/size는 1이다. 응답의 종목·단위·정확한 시작 시각·원본 금액을 검사한다. 이전 봉이나 진행 중인 봉은 저장하지 않는다.
- SQLite에 먼저 해당 거래소·시간의 작업과 종목 목록을 예약한다. 재시작해도 이미 시도한 작업은 재요청하지 않는다. 실패·중단·빈 응답은 결측이다.
- 418/429/5xx는 해당 시간의 남은 요청을 멈춘다. 실패 요청 재시도와 과거 백필은 없다.
- `data/index-hours.sqlite3`에 시가·수량·해당 시간의 원화 거래금액을 십진 문자열로 저장한다. 동일 거래소·종목·시간은 한 행이다.
- 디지털엑스·고팍스의 시간봉 원화 거래금액은 미제공으로 남긴다. 티커·현재가·종가로 추정하지 않는다. 두 거래소의 실시간 체결·24시간 티커는 기존대로 사용한다.

## 조회

- `GET /api/market-share/index-day?source=ticker_24h`: 오늘의 공식 최근 24시간 티커.
- `GET /api/market-share/index-day?day=YYYY-MM-DD&source=hourly_rolling`: 오늘은 최근 끝난 24시간 구간 중 저장한 시간봉의 합계.
- 과거 날짜는 KST 00:00~다음 날 00:00의 저장된 시간봉만 읽는다. 조회는 수집이나 DB 쓰기를 시작하지 않는다.
- 24시간 모두 확인한 종목만 COMPLETE다. 일부 금액은 확인한 합계와 PARTIAL로 보여 준다. 결측은 null, 원본에서 확인한 0만 0이다.
- `projection=domestic_grid`, 코인·거래소·KRW 필터도 동일한 원본과 분모를 쓴다.
- UI의 자료 선택으로 티커와 저장한 시간봉을 구분한다. 날짜를 바꾸면 해당 날짜의 저장 자료를 읽는다.

## 업데이트

프로그램을 완전히 종료하고 `data` 폴더 전체를 새 플랫폼 폴더로 옮긴다. 같은 SQLite 스키마라 Windows와 Mac 사이에도 옮길 수 있다.
개인 기록은 Git과 배포 ZIP에서 제외한다. 실시간 체결 메모리는 계속 실행 시점부터 쌓이며 영구 저장 대상이 아니다.

회귀 검사는 파서·정시 경계·KST 날짜·동일 시간 중복·재시작·잠자기 구간 건너뛰기·실패/미제공/null/0·HTTP·수집기 자동 시작을 다룬다.
실제 Windows BAT 실행과 1시간 이상 지속 실행은 이번 변경에서 별도 검증하지 않았다.

공식 계약: [업비트](https://docs.upbit.com/kr/reference/list-candles-minutes), [빗썸](https://apidocs.bithumb.com/reference/분minute-캔들), [코인원](https://docs.coinone.co.kr/v1.1/reference/chart).
Mac 런타임: [python-build-standalone 20261026](https://github.com/astral-sh/python-build-standalone/releases/tag/20261026).

PC 절전·시계 변경으로 프로세스 시각과 실제 경과 시각이 달라지면 공식 HTTPS UTC를 다시 확인한다.
절전 중 지나간 시간의 요청을 이어서 저장하지 않고, 현재 최신 종료 구간으로 건너뛴다. UTC 확인 실패 시 시세 요청을 보류한다.
