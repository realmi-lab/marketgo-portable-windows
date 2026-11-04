"""Package identical app/web sources with pinned Windows or macOS Python."""
import argparse
from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import tarfile
import zipfile

ROOT = Path(__file__).resolve().parents[2]
SOURCE = Path(__file__).resolve().parent
CACHE = ROOT / '.tmp' / 'python-runtime'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def fetch(record):
    CACHE.mkdir(parents=True, exist_ok=True)
    path = CACHE / record['filename']
    if not path.exists():
        subprocess.run(['curl', '--fail', '--location', '--silent', '--show-error', '--max-time', '25',
                        record['url'], '-o', str(path)], check=True, timeout=28)
    if digest(path) != record['sha256']:
        raise RuntimeError(f'Dependency SHA256 mismatch: {path.name}')
    return path


def extract(archive, directory):
    directory.mkdir(parents=True, exist_ok=True)
    base = directory.resolve()
    with zipfile.ZipFile(archive) as z:
        for member in z.infolist():
            target = (directory / member.filename).resolve()
            if not target.is_relative_to(base) or (member.external_attr >> 16) & 0o170000 == 0o120000:
                raise RuntimeError(f'Unsafe dependency ZIP member: {member.filename}')
        z.extractall(directory)


def build(platform, web, dependencies):
    output = ROOT / 'dist' / ('windows-python-portable' if platform == 'windows' else 'macos-python-portable')
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='build-', dir=output) as staging:
        staging = Path(staging)
        bundle = staging / 'MarketGO'
        bundle.mkdir()
        shutil.copytree(web, bundle / 'web')
        if platform == 'windows':
            extract(fetch(dependencies['python']), bundle / 'runtime')
            pth = next((bundle / 'runtime').glob('python*._pth'))
            standard_zip = next((bundle / 'runtime').glob('python*.zip')).name
            pth.write_text(f'{standard_zip}\n.\n../app\n../vendor\n', encoding='ascii')
        else:
            with tarfile.open(fetch(dependencies['python_macos_arm64'])) as archive:
                archive.extractall(staging / 'unpacked', filter='data')
            # Materialize aliases so the ZIP needs no symlink support.
            shutil.copytree(staging / 'unpacked' / 'python', bundle / 'runtime', symlinks=False)
        for wheel in dependencies['wheels']:
            extract(fetch(wheel), bundle / 'vendor')
        (bundle / 'app').mkdir()
        for name in ('adapters.py', 'engine.py', 'collector.py', 'server.py', 'market_clock.py', 'hourly.py', 'notices.py'):
            shutil.copy2(SOURCE / name, bundle / 'app' / name)
        shutil.copy2(SOURCE / 'dependencies.json', bundle / 'DEPENDENCIES.json')
        if platform == 'windows':
            shutil.copy2(ROOT / 'docs' / 'portable-windows.md', bundle / 'README-KO.md')
            bat = (ROOT / 'scripts' / 'start-windows.bat').read_text()
            (bundle / 'start.bat').write_bytes(bat.replace('\r\n', '\n').replace('\n', '\r\n').encode('ascii'))
            required = ('runtime/python.exe', 'runtime/_ssl.pyd', 'runtime/_socket.pyd', 'runtime/_sqlite3.pyd')
            name = 'MarketGO-Windows-x64.zip'
        else:
            shutil.copy2(ROOT / 'docs' / 'portable-macos.md', bundle / 'README-KO.md')
            shutil.copy2(ROOT / 'scripts' / 'Start-MarketGO.command', bundle / 'Start-MarketGO.command')
            (bundle / 'Start-MarketGO.command').chmod(0o755)
            required = ('runtime/bin/python3',)
            name = 'MarketGO-macOS-arm64.zip'
        for required_file in (*required, 'web/index.html', 'vendor/websocket/__init__.py', 'vendor/certifi/cacert.pem', 'app/hourly.py'):
            if not (bundle / required_file).is_file():
                raise RuntimeError(f'Missing runtime dependency: {required_file}')
        archive = output / name
        with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as z:
            for path in sorted(bundle.rglob('*')):
                if path.is_symlink():
                    raise RuntimeError(f'Symlink is not a portable asset: {path}')
                if path.is_file():
                    z.write(path, Path('MarketGO') / path.relative_to(bundle))
        with zipfile.ZipFile(archive) as z:
            if z.testzip():
                raise RuntimeError('ZIP integrity verification failed')
        final = output / 'MarketGO'
        if final.exists():
            shutil.rmtree(final)
        shutil.copytree(bundle, final)
        (output / (archive.name + '.sha256')).write_text(f'{digest(archive)}  {archive.name}\n')
        print(f'ZIP: {archive} ({archive.stat().st_size:,} bytes)')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--platform', choices=('windows', 'mac-arm64', 'all'), default='windows')
    args = parser.parse_args()
    dependencies = json.loads((SOURCE / 'dependencies.json').read_text())
    output = ROOT / 'dist'
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='shared-web-', dir=output) as staging:
        web = Path(staging) / 'web'
        environment = dict(os.environ, VITE_PORTABLE_DESKTOP='true', VITE_PORTABLE_POWERSHELL='false',
                           VITE_PORTABLE_PYTHON='true', VITE_LLM_ENABLED='false', VITE_API_URL='')
        subprocess.run(['pnpm', 'exec', 'vite', 'build', '--base', '/', '--outDir', str(web)],
                       cwd=ROOT / 'frontend', env=environment, check=True, timeout=30)
        for platform in ('windows', 'mac-arm64') if args.platform == 'all' else (args.platform,):
            build(platform, web, dependencies)


if __name__ == '__main__':
    main()
