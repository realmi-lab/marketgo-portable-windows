# MarketGO Portable 소스

Windows Python 서버와 Apple Silicon Mac Go 서버, 공통 React 화면의 빌드 원본입니다.
빌드에 필요한 Go 패키지와 임베드 리소스를 포함합니다. 운영 배포 설정, 인증값, 수집 기록은 포함하지 않습니다.
공지·AI 관련 Go 코드 일부는 공통 서버의 컴파일 의존성으로 포함되지만 포터블 실행기에서는 해당 기능을 끕니다.

## 다시 빌드

Mac 또는 Linux에서 Go 1.26 이상, Node.js 26, pnpm 11.10.0, Python 3.10 이상,
`curl`, `zip`을 준비합니다. 실행용 ZIP에는 개발 도구 설치가 필요하지 않습니다.
이 README가 있는 `source/`를 기준으로 실행합니다.

```sh
cd frontend
pnpm install --frozen-lockfile
cd ..
sh scripts/build-portable-all.sh
```

통합 ZIP: `dist/universal-portable/MarketGO-Portable.zip`.
SHA256: 같은 폴더의 `MarketGO-Portable.zip.sha256`.
통합 스크립트는 Windows와 Apple Silicon Mac만 생성합니다.
서버를 실행하거나 기존 수집 프로그램을 종료하지 않습니다.

개별 빌드:

```sh
sh scripts/build-portable-windows.sh
sh scripts/build-portable-macos.sh arm64
```

Windows 빌드는 `dist/windows-python-portable/`, Mac 빌드는 `dist/macos-native-portable/`에 생성합니다.
Windows 런타임·라이브러리 다운로드는 `scripts/portable-python/dependencies.json`에 고정된 SHA256으로 검사합니다.
다운로드 캐시는 `.tmp/python-runtime/`입니다. Windows용 ZIP은 Windows 기본 BAT로 실행하며 Python 설치가 필요하지 않습니다.
Go 모듈은 `go.sum`, 화면 패키지는 `frontend/pnpm-lock.yaml`로 고정합니다.
ZIP 생성 시각과 Go 빌드 환경에 따라 압축 파일·바이너리 해시는 달라질 수 있습니다.

## 회귀 검사

화면 타입과 포터블에 관련된 결정론적 검사:

```sh
cd frontend
pnpm check
pnpm test
cd ..
```

Python 검사는 별도의 개발용 가상환경에 고정된 테스트 의존성을 설치해 실행합니다.
다음 명령은 Mac/Linux 기준입니다.

```sh
python3 -m venv .venv
.venv/bin/python -m pip install --require-hashes -r tests/portable_python/requirements.txt
PYTHONPATH=scripts/portable-python .venv/bin/python -m unittest discover -s tests/portable_python -v
```

17개 Python 검사는 중복 체결, 구간·코인별 분모, 누락과 0 구분, 재연결,
보관 한도, HTTP 응답, PC 시각 오차·단조 시간 및 HTTPS 검증을 확인합니다.
외부 거래소의 현재 장애나 모든 PC의 보안 정책을 검증하는 검사는 아닙니다.

## 코드 위치

- `scripts/portable-python/`: Windows 수집·계산·HTTP 서버와 패키지 생성기
- `scripts/start-windows.bat`: Windows 실행기
- `scripts/Start-MarketGO.command`: Mac 실행기
- `frontend/client/`: 두 플랫폼의 공통 화면. 빌드 플래그로 Python REST 또는 Go 연결을 선택합니다.
- `cmd/market-share/`, `internal/`, `services/`, `contracts/`: Mac 서버와 필요한 Go 패키지
- `docs/`: 플랫폼별 실행 안내
- `SOURCE-MANIFEST.json`: 공개한 원본 파일의 SHA256 목록

서버의 저장 모드는 메모리입니다. 배포용 Apple 개발자 서명·공증은 하지 않았습니다.
다른 서비스의 운영·DB 관리 도구와 전체 제품의 E2E 하네스는 이 포터블 소스 배포 범위에 포함하지 않습니다.
