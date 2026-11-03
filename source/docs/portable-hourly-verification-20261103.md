# 공통 Python 포터블·자동 시간봉 인덱스 (2026-11-03)

Windows x64와 Apple Silicon Mac 패키지가 같은 Python 앱 6개와 같은 React 화면을 사용한다.
실행하면 최근 종료 시간봉을 한 번 수집하고 이후 매 정시 2분 뒤 새 시간봉을 저장한다.
로컬 SQLite 영구 저장·날짜별 조회·자료 선택·부분 자료 표시를 연결했다.
개인 data/는 Git과 ZIP에서 제외하며 과거 결측/실패 백필은 활성화하지 않는다.

검증: 포함된 Mac Python 3.14.10에서 공통 회귀 24개 통과, 프론트 단위 32개와 타입 검사 통과.
실제 최신 BTC 시간봉 3개 거래소의 시가·거래금액 파싱 확인.
Mac 실행기·내장 런타임으로 자동 저장, 브라우저 자료 전환, 과거 날짜 조회를 확인했다.
브라우저 오류 0건. 두 ZIP의 app/와 web/ 바이트 동일, 통합 ZIP 무결성 확인.
실제 Windows BAT 실행과 1시간 이상 지속 실행은 이번 변경에서 확인하지 않았다.

최종 Mac 짧은 실행 결과:
```json
{
  "platform": "mac-arm64",
  "version": "3.2.0",
  "stored_candles": 15,
  "hourly_rows": 1442,
  "history_day": "2026-11-02",
  "page_errors": []
}
```

배포 ZIP SHA256: `9d1563e02d0b326bea9654f037a5693fe91a2e1425804899eee3b970ad59d645`.
