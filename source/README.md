# MarketGO Portable 소스

Windows와 Apple Silicon Mac이 같은 Python 수집·계산·HTTP·시간봉 저장 코드와 같은 React 화면을 사용합니다.
플랫폼별로 런타임과 실행기만 다릅니다. 예전 Go 소스는 참고용으로 보존하며 현재 패키지에는 사용하지 않습니다.
운영 설정·인증값·사용자 수집 기록은 포함하지 않습니다.

## 빌드

Node.js 26, pnpm 11.10.0, Python 3.12 이상, curl이 필요합니다. 사용자용 ZIP에는 개발 도구 설치가 필요하지 않습니다.
이 폴더에서 실행합니다.

```sh
cd frontend
pnpm install --frozen-lockfile
cd ..
sh scripts/build-portable-all.sh
```

통합 ZIP: `dist/universal-portable/MarketGO-Portable.zip`과 `.sha256`.
빌드는 서버를 실행하지 않습니다. Windows·Mac의 app/와 web/ 파일이 다르면 통합 빌드가 실패합니다.
런타임·라이브러리는 `scripts/portable-python/dependencies.json`의 SHA256으로 검사합니다.
Windows는 Python.org 임베디드 CPython, Mac은 Astral python-build-standalone CPython이며 둘 다 3.14.10입니다.

개별 빌드는 `sh scripts/build-portable-windows.sh`, `sh scripts/build-portable-macos.sh arm64`입니다.
출력은 각각 `dist/windows-python-portable/`, `dist/macos-python-portable/`입니다.

## 검증

```sh
python3 -m venv .venv
.venv/bin/python -m pip install --require-hashes -r ../requirements.txt
PYTHONPATH=scripts/portable-python .venv/bin/python -m unittest discover -s tests/portable_python -v
cd frontend
pnpm check
pnpm test
```

24개 Python 검사는 체결·구간 계산·중복·결측·시간 보정과 공식 시간봉 경계·재시작·영구 저장·자동 수집기 시작·HTTP를 검사합니다.
실제 Windows의 이번 BAT 실행과 장시간 수집은 별도 검증이 필요합니다.

## 위치

- `scripts/portable-python/`: 두 플랫폼이 함께 쓰는 프로그램과 패키지 생성기.
- `scripts/start-windows.bat`, `scripts/Start-MarketGO.command`: 플랫폼 실행기.
- `frontend/client/`: 공통 화면. 두 플랫폼 모두 VITE_PORTABLE_PYTHON=true.
- `tests/portable_python/`: 공통 회귀 검사.
- `docs/hourly-index.md`: 수집·저장·결측 계약.
- `SOURCE-MANIFEST.json`: 이 파일 자체를 제외한 소스 SHA256 목록.

실시간 체결은 메모리, 공식 시간봉은 실행 폴더의 data/index-hours.sqlite3에 저장합니다.
과거 결측 백필·실패 재시도·공지·AI는 실행하지 않습니다. 사용자 data/는 Git과 배포 ZIP에서 제외합니다.
