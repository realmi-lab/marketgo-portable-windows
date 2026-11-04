# MarketGO Portable — Windows · Mac 통합판

[통합 ZIP 다운로드](https://raw.githubusercontent.com/realmi-lab/marketgo-portable-windows/main/build/MarketGO-Portable.zip)

ZIP 하나에 Windows와 Apple Silicon Mac용 프로그램이 들어 있습니다. Intel Mac용은 포함하지 않습니다.
압축을 푼 뒤 자신의 컴퓨터에 맞는 폴더에서 실행하세요. 별도 Python·Go·Node·DB 설치는 필요하지 않습니다.
거래소 데이터 수신에는 인터넷 연결이 필요합니다.

## 실행 방법

1. 이전에 켜둔 MarketGO 실행 창을 먼저 종료합니다.
2. **ZIP 전체를 새 폴더에 압축 해제**합니다. ZIP 안에서 실행 파일만 열지 마세요.
3. 아래 표에서 자신의 컴퓨터에 해당하는 파일을 더블클릭합니다.

| 컴퓨터 | 열 폴더 | 실행 파일 |
|---|---|---|
| Windows 10/11, 64비트 x64 | `MarketGO/Windows` | `start.bat` |
| Apple Silicon Mac — M 시리즈 | `MarketGO/Mac-AppleSilicon` | `Start-MarketGO.command` |

Mac 종류는 Apple 메뉴 → **이 Mac에 관하여**에서 확인합니다.
'칩: Apple M…'이면 Apple Silicon용을 실행하세요.

준비되면 기본 브라우저가 열립니다. 자동으로 열리지 않으면 **http://127.0.0.1:20560/** 를 직접 여세요.
이 주소는 프로그램을 실행한 PC의 주소입니다.

**실행 창을 유지해야 수집이 계속됩니다.** 종료하려면 실행 창에서 `Ctrl+C`를 누릅니다.
브라우저만 닫으면 수집 프로그램은 계속 실행될 수 있습니다. 한 PC에서는 실행 창 하나만 사용하세요.
각 폴더 안의 파일은 함께 유지해야 합니다.

## 데이터와 보관

- 오늘 코인 보기: 국내 5개 거래소의 공식 최근 24시간 원화 거래대금을 자동 갱신합니다.
- 실시간 보기·그래프: 이 PC에서 실제 수신한 체결로 계산합니다. 디지털엑스·고팍스를 포함합니다.
- 실행하면 최근에 끝난 공식 1시간봉을 자동 수집하고 이후 매 정시 2분 뒤 새 시간봉을 수집합니다. 빗썸·업비트·코인원의 원화 거래금액만 사용합니다.
- ‘자료 → 수집한 시간봉’에서 저장된 인덱스를 봅니다. 과거 날짜는 그 한국 날짜의 시간봉을 읽습니다. 일부만 저장됐다면 부분 자료로 표시합니다.
- 시간봉은 각 플랫폼 폴더의 `data/index-hours.sqlite3`에 남습니다. 업데이트할 때 프로그램을 완전히 종료한 뒤 `data` 폴더 전체를 새 폴더로 옮기세요.
- 실시간 체결 이력은 종료하면 사라집니다. 다시 켜면 24시간 티커를 새로 받고, 실시간 체결은 실행 시점부터 다시 쌓입니다.
- 종료·잠자기·연결 중단 중의 체결을 복구하지 않습니다. 미수신 값을 실제 0으로 채우지 않습니다.
- 상단 **공지 타임라인**에서 공지 일정·본문·관련 코인 거래량을 봅니다. 공지·일정은 기존 운영 공지 서버에서 읽고, 거래량은 이 PC가 직접 수신한 최근 10분 체결·저장한 시간봉입니다. **시장 인덱스**를 누르면 대시보드로 돌아옵니다.
- LLM 메뉴·로컬 AI 작업은 실행하지 않습니다.
- 과거 결측과 실패 요청을 자동 복구하지 않습니다. 디지털엑스·고팍스의 시간봉 거래금액은 미제공으로 남깁니다.

## 플랫폼별 구성과 확인 범위

- Windows와 Mac은 동일한 `app/*.py`와 `web/*`를 사용합니다. 통합 ZIP 생성 시 파일 내용이 다르면 빌드를 중단합니다.
- **Windows**: Python.org의 공식 x64 임베디드 런타임을 포함합니다. 이번 시간봉 저장 변경은 실제 Windows에서 아직 실행 검증하지 않았습니다.
- **Mac**: Astral python-build-standalone의 Apple Silicon Python 런타임을 포함합니다. 별도 설치가 필요하지 않습니다. macOS 보안 확인이 표시될 수 있습니다.
- 두 런타임은 같은 Python 3.14.10이며 고정된 다운로드와 SHA256을 확인합니다. 수집·계산·저장·시각 보정 로직은 공통입니다.

## 오류가 날 때

- 먼저 이전 MarketGO 실행 창이 남아 있는지 확인하세요. 같은 포트의 프로그램이 이미 켜져 있으면 새 실행이 실패할 수 있습니다.
- Windows 오류 기록: `Windows/logs/launcher.log`, `Windows/logs/runtime.log`.
- Mac 오류 기록: `Mac-AppleSilicon/logs/runtime.log`와 실행한 터미널 창의 출력.
- Windows에서 시스템 날짜가 크게 틀려 HTTPS 인증서 오류가 나면 자동 시각 동기화를 확인한 뒤 다시 실행하세요.
- 각 플랫폼 폴더의 `README-KO.md`에도 세부 설명이 있습니다.

## 소스코드와 빌드

[소스코드](https://github.com/realmi-lab/marketgo-portable-windows/tree/main/source)에는 공통 Python 서버,
React 화면과 플랫폼별 패키지 생성기가 들어 있습니다. 예전 Go 소스는 참고용으로 남아 있으며 현재 패키지에서 실행하지 않습니다.
포터블 실행에는 개발 도구가 필요하지 않습니다. 아래 도구는 직접 다시 빌드할 때만 필요합니다.
Python 개발·검사용 패키지와 버전은 저장소 최상단의 `requirements.txt`에 있습니다.
저장소 루트에서 `python -m pip install -r requirements.txt`로 설치할 수 있습니다.

```text
build/MarketGO-Portable.zip     Windows + Apple Silicon Mac 통합 포터블
source/scripts/portable-python/ Windows·Mac 공통 수집·계산·HTTP·시간봉 저장
source/frontend/               공통 화면 원본과 잠금 파일
source/docs/hourly-index.md    자동 시간봉 수집·보관 계약
source/scripts/                실행기와 빌드 스크립트
source/tests/portable_python/   수집·계산·시각 회귀 검사
```

**Mac 또는 Linux에서 재빌드**: Node.js 26, pnpm 11.10.0,
Python 3.12 이상, curl, zip이 필요합니다. 개발 의존성 다운로드에도 인터넷이 필요합니다.

```sh
cd source/frontend
pnpm install --frozen-lockfile
cd ..
sh scripts/build-portable-all.sh
```

결과는 `source/dist/universal-portable/MarketGO-Portable.zip`입니다.
빌드 스크립트는 서버를 실행하지 않습니다. Windows 공식 Python과 라이브러리는 고정된 SHA256으로 확인합니다.
화면 의존성은 `pnpm-lock.yaml`을 사용합니다.
검사 명령과 소스 범위는 [소스 README](https://github.com/realmi-lab/marketgo-portable-windows/blob/main/source/README.md)에 있습니다.

[저장소](https://github.com/realmi-lab/marketgo-portable-windows)
