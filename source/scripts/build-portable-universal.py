"""Combine verified per-platform releases without changing any runtime bytes."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import stat
import zipfile

ROOT = Path(__file__).resolve().parents[1]
PLATFORMS = {
    'Windows': 'MarketGO-Windows-x64.zip',
    'Mac-AppleSilicon': 'MarketGO-macOS-arm64.zip',
}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source', type=Path, required=True)
    args = parser.parse_args()
    output = ROOT / 'dist' / 'universal-portable'
    output.mkdir(parents=True, exist_ok=True)
    target = output / 'MarketGO-Portable.zip'
    readme = (ROOT / 'docs' / 'portable-all-platforms.md').read_text(encoding='utf-8')
    notices = (ROOT / 'docs' / 'portable-third-party-notices.txt').read_bytes()
    expected, sources = {}, {}
    with zipfile.ZipFile(target, 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as combined:
        root_info = zipfile.ZipInfo('MarketGO/README-KO.txt')
        root_info.create_system = 3
        root_info.external_attr = (stat.S_IFREG | 0o644) << 16
        root_info.compress_type = zipfile.ZIP_DEFLATED
        combined.writestr(root_info, readme.encode('utf-8-sig'))
        combined.writestr('MarketGO/THIRD-PARTY-NOTICES.txt', notices)
        for platform, name in PLATFORMS.items():
            source = args.source / name
            sources[platform] = hashlib.sha256(source.read_bytes()).hexdigest()
            with zipfile.ZipFile(source) as package:
                for member in package.infolist():
                    if member.is_dir():
                        continue
                    path = PurePosixPath(member.filename)
                    if not member.filename.startswith('MarketGO/') or '..' in path.parts or path.is_absolute():
                        raise ValueError(f'Unexpected release entry: {member.filename}')
                    mode = member.external_attr >> 16
                    if stat.S_ISLNK(mode):
                        raise ValueError(f'Symlink in release: {member.filename}')
                    relative = str(path.relative_to('MarketGO'))
                    destination = f'MarketGO/{platform}/{relative}'
                    if destination in expected:
                        raise ValueError(f'Duplicate release entry: {destination}')
                    data = package.read(member)
                    expected[destination] = hashlib.sha256(data).hexdigest()
                    info = zipfile.ZipInfo(destination, date_time=member.date_time)
                    info.create_system = 3
                    info.external_attr = member.external_attr
                    info.compress_type = zipfile.ZIP_DEFLATED
                    combined.writestr(info, data)
    with zipfile.ZipFile(target) as result:
        if result.testzip():
            raise ValueError('Combined ZIP failed integrity check')
        actual_files = {i.filename for i in result.infolist() if not i.is_dir()}
        assert actual_files == set(expected) | {'MarketGO/README-KO.txt', 'MarketGO/THIRD-PARTY-NOTICES.txt'}
        for name, digest in expected.items():
            assert hashlib.sha256(result.read(name)).hexdigest() == digest, name
        for platform in ('Mac-AppleSilicon',):
            for file in ('Start-MarketGO.command', 'runtime/bin/python3'):
                info = result.getinfo(f'MarketGO/{platform}/{file}')
                assert (info.external_attr >> 16) & stat.S_IXUSR, info.filename
        assert result.read('MarketGO/Windows/start.bat').startswith(b'@echo off\r\n')
        assert result.read('MarketGO/README-KO.txt').decode('utf-8-sig') == readme
        assert result.read('MarketGO/THIRD-PARTY-NOTICES.txt') == notices
        for prefix in ('app/', 'web/'):
            windows = {n.removeprefix('MarketGO/Windows/') for n in actual_files if n.startswith('MarketGO/Windows/' + prefix)}
            mac = {n.removeprefix('MarketGO/Mac-AppleSilicon/') for n in actual_files if n.startswith('MarketGO/Mac-AppleSilicon/' + prefix)}
            if windows != mac or not windows:
                raise ValueError(f'Platform source file lists differ: {prefix}')
            for name in windows:
                if result.read('MarketGO/Windows/' + name) != result.read('MarketGO/Mac-AppleSilicon/' + name):
                    raise ValueError(f'Platform application bytes differ: {name}')
    receipt = {'archive': target.name, 'sha256': hashlib.sha256(target.read_bytes()).hexdigest(),
               'sources': sources, 'verified_runtime_files': len(expected)}
    (output / 'verification.json').write_text(json.dumps(receipt, indent=2) + '\n')
    (output / (target.name + '.sha256')).write_text(f'{receipt["sha256"]}  {target.name}\n')
    print(json.dumps(receipt, indent=2))
    print(f'ZIP: {target} ({target.stat().st_size:,} bytes)')


if __name__ == '__main__':
    main()
