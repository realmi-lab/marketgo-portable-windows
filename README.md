# MarketGO Portable — Windows · Mac 통합판

[통합 ZIP 다운로드](https://raw.githubusercontent.com/realmi-lab/marketgo-portable-windows/main/MarketGO-Portable.zip)

ZIP 하나에 Windows, Apple Silicon Mac, Intel Mac용 프로그램이 들어 있습니다.
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
| Intel Mac | `MarketGO/Mac-Intel` | `Start-MarketGO.command` |

Mac 종류는 Apple 메뉴 → **이 Mac에 관하여**에서 확인합니다.
'칩: Apple M…'이면 Apple Silicon용, '프로세서: Intel…'이면 Intel용을 실행하세요.

준비되면 기본 브라우저가 열립니다. 자동으로 열리지 않으면 **http://127.0.0.1:20560/** 를 직접 여세요.
이 주소는 프로그램을 실행한 PC의 주소입니다.

**실행 창을 유지해야 수집이 계속됩니다.** 종료하려면 실행 창에서 `Ctrl+C`를 누릅니다.
브라우저만 닫으면 수집 프로그램은 계속 실행될 수 있습니다. 한 PC에서는 실행 창 하나만 사용하세요.
각 폴더 안의 파일은 함께 유지해야 합니다.

## 데이터와 보관

- 오늘 코인 보기: 국내 5개 거래소의 공식 최근 24시간 원화 거래대금을 자동 갱신합니다.
- 실시간 보기·그래프: 이 PC에서 실제 수신한 체결로 계산합니다. 디지털엑스·고팍스를 포함합니다.
- 종료하면 메모리의 수집 이력은 사라집니다. 다시 켜면 24시간 티커를 새로 받고, 실시간 체결은 실행 시점부터 다시 쌓입니다.
- 종료·잠자기·연결 중단 중의 체결을 복구하지 않습니다. 미수신 값을 실제 0으로 채우지 않습니다.
- 공지 수집과 AI 작업은 꺼져 있습니다.

## 플랫폼별 구성과 확인 범위

- **Windows**: 공식 Python 런타임이 포함된 버전입니다. 프로그램 내부에서 공식 서버 기준으로 수집 시각을 보정합니다.
  실제 Windows에서 BAT 실행, 한글·공백·특수문자 경로, 5개 거래소 수신, 실시간 갱신, 재실행, 시각 오차 재현 검사를 통과했습니다.
- **Mac**: 설치 없이 실행하는 네이티브 버전입니다. Apple Silicon은 실제 데이터 수신을 확인했습니다.
  Intel은 교차 빌드·실행 파일 형식 검사까지 완료했으며 Intel Mac 실기 실행은 미검증입니다.
  배포용 Apple 개발자 서명·공증은 하지 않아 macOS 보안 확인이 표시될 수 있습니다.

## 오류가 날 때

- 먼저 이전 MarketGO 실행 창이 남아 있는지 확인하세요. 같은 포트의 프로그램이 이미 켜져 있으면 새 실행이 실패할 수 있습니다.
- Windows 오류 기록: `Windows/logs/launcher.log`, `Windows/logs/runtime.log`.
- Mac 오류 기록: 실행한 터미널 창의 출력.
- Windows에서 시스템 날짜가 크게 틀려 HTTPS 인증서 오류가 나면 자동 시각 동기화를 확인한 뒤 다시 실행하세요.
- 각 플랫폼 폴더의 `README-KO.md`에도 세부 설명이 있습니다.

[저장소](https://github.com/realmi-lab/marketgo-portable-windows)
