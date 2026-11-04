# 공지 타임라인 포터블 3.3.0 검증 — 2026-11-04

## 변경

- 공통 포터블에 공지 타임라인 메뉴·진입 경로·대시보드 복귀를 추가했습니다.
- 공지 목록·일정은 운영 공개 공지 서버에서 읽습니다. 공지와 시세의 출처를 화면에 표시합니다.
- 최근 10분 거래금액·체결 수·거래 수량은 로컬 수신 체결에서 계산합니다. 원격 거래량을 섞지 않습니다.
- 시간봉은 기존 로컬 SQLite를 읽으며 과거 시세 복구·재시도는 활성화하지 않았습니다.
- 목록 버전 충돌 시 캐시를 비우고 새 목록을 읽도록 처리했습니다.
- 모바일 안내문의 세로 줄바꿈을 확인·수정했습니다.

## 수행한 검증

- Python 공통 회귀 30개 통과 후, 추가 버전 충돌 검사를 포함한 최종 공지 관련 7개 재검사 통과.
- 프론트 타입 검사 통과. 공지·보관 계약 테스트 17개 통과.
- Windows/Mac 통합 빌드·ZIP 무결성·app/web 내용 동일 검사 통과.
- 최종 ZIP의 7개 Python 앱 소스가 현재 소스와 두 플랫폼 모두 일치. data/ 미포함 확인.
- 실제 Mac 패키지 실행: 목록·일정·코인 선택·양수 거래수량/금액·공식 시간봉 API·로컬 복귀·공지 직접 새로 열기 통과.
- 1440px·390px 캡처 확인. 브라우저 오류·실패 API 0건. LLM 메뉴 없음.

실제 패키지 관측값:

```json
{
  "version": "3.3.0",
  "notices": true,
  "archive_label": "전체 353",
  "timeline_items": 37,
  "selected_asset": "POD",
  "local_trade_count": 1,
  "local_quantity": 1629.87608174,
  "local_amount": 1070828.58570318,
  "hourly_contract": "INDEX_DAY_HOURLY_V1",
  "local_navigation": true,
  "page_errors": [],
  "failed_api": []
}
```

ZIP 검증 결과:

```json
{
  "archive": "MarketGO-Portable.zip",
  "sha256": "bcf1989cfe53da080631c9dd4526e44e1ef1efc435088240f6de75f028ab2a2b",
  "sources": {
    "Windows": "774fd7dfc2b76945a95b51f78fe5fcb212608c19f90bbf03a1234e1a00d26d5a",
    "Mac-AppleSilicon": "cb0f6e57ac7f53c8ab9bf8f85ebe6f5d1f79c8ac47fb67be305cc659d1bd0ddf"
  },
  "verified_runtime_files": 2148
}
```

## 범위

- 실제 Windows BAT 실행, 장시간 수집, 절전 후 실제 기기 동작은 이번 검증에 포함하지 않았습니다.
- 운영 서버·컨테이너·DB는 변경하지 않았습니다. 공지 API는 읽기 요청만 했습니다.
- 샘플 거래량은 실행 직후의 부분 관측값이며 10분 전체 또는 성능 측정값이 아닙니다.
- 점검용 포트 21663의 프로세스는 종료했습니다.
