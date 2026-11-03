# MarketGO Portable — Apple Silicon Mac / Python

`Start-MarketGO.command`를 더블클릭하면 포함된 Python 런타임이 수집·계산 서버와 화면을 엽니다.
Windows와 동일한 Python 소스·React 화면을 사용합니다. 별도 Python·Go·Node·DB 설치는 필요하지 않습니다.
Apple Silicon(M 시리즈)용입니다. 종료는 실행 터미널에서 Ctrl+C입니다.

- 로컬 주소: http://127.0.0.1:20560/
- 오늘 코인 보기: 국내 5개 거래소의 공식 최근 24시간 원화 티커.
- 실시간 보기: 이 PC에서 받은 체결만 계산. 종료하면 메모리 이력은 사라집니다.
- 시간봉: 실행 시 최근에 끝난 시간봉과 이후 새 시간봉을 자동 수집합니다. 정시 종료 2분 뒤부터 수집합니다.
- 자료 선택의 ‘수집한 시간봉’에서 저장 인덱스를 봅니다. 과거 날짜는 저장한 KST 날짜의 봉만 합산합니다.
- 빗썸·업비트·코인원의 시간봉 원화 거래금액을 저장합니다. 디지털엑스·고팍스의 미제공 시간봉 거래금액은 빈 값으로 둡니다.
- 과거 결측·실패·꺼져 있던 시간의 백필은 하지 않습니다. 일부 시간만 있으면 부분 자료입니다.
- 저장: `data/index-hours.sqlite3`. 종료 후에도 유지됩니다. 업데이트할 때 프로그램을 완전히 종료하고 `data` 폴더 전체를 새 Mac 폴더에 옮기세요.
- 오류: `logs/runtime.log`와 터미널 출력. 같은 PC에서는 실행 창 하나만 사용하세요.
- 공지와 AI 기능은 꺼져 있습니다.

`runtime/`는 SHA256으로 고정한 Astral python-build-standalone의 CPython 3.14.10입니다.
인증서 검증을 유지하며 수집 시각을 공식 HTTPS 서버 기준으로 보정합니다. OS 시각을 변경하지 않습니다.
`app/`와 `web/`는 Windows 패키지와 내용이 같습니다. `data/`는 사용자 기록이므로 배포 ZIP에 넣지 않습니다.
macOS 보안 확인이 표시될 수 있습니다.

소스 루트에서 `sh scripts/build-portable-macos.sh arm64`로 빌드합니다. 출력은 `dist/macos-python-portable/`입니다.
