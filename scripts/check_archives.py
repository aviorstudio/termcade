"""Validate the complete native archive set and its embedded source receipt."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import stat
import subprocess
import tarfile
import tempfile
import zipfile

MATRIX=[('Linux','x86_64','tar.gz','x86_64-unknown-linux-musl'),('Linux','arm64','tar.gz','aarch64-unknown-linux-musl'),('Darwin','x86_64','tar.gz','x86_64-apple-darwin'),('Darwin','arm64','tar.gz','aarch64-apple-darwin'),('Windows','x86_64','zip','x86_64-pc-windows-msvc')]
def host_archive():
    system=platform.system();arch={'amd64':'x86_64','aarch64':'arm64'}.get(platform.machine().lower(),platform.machine().lower())
    return Path('dist')/f'termcade_{system}_{arch}.{"zip"if system=="Windows"else"tar.gz"}'
def extract(archive,directory):
    directory=Path(directory);directory.mkdir(parents=True,exist_ok=True)
    name='termcade.exe'if archive.suffix=='.zip'else'termcade'
    if archive.suffix=='.zip':
        with zipfile.ZipFile(archive)as package:
            items=package.infolist()
            assert len(items)==1 and items[0].filename==name and not items[0].is_dir()
            mode=items[0].external_attr>>16
            assert stat.S_IFMT(mode) in (0,stat.S_IFREG),'ZIP entry is not a regular file'
            package.extractall(directory)
    else:
        with tarfile.open(archive)as package:
            items=package.getmembers()
            assert len(items)==1 and items[0].name==name and items[0].isfile()
            package.extractall(directory,filter='data')
    binary=directory/name
    assert binary.is_file()and not binary.is_symlink()
    binary.chmod(binary.stat().st_mode|stat.S_IXUSR)
    return binary

def main():
    parser=argparse.ArgumentParser();parser.add_argument('--record',action='store_true');args=parser.parse_args()
    expected={f'termcade_{system}_{arch}.{extension}'for system,arch,extension,_ in MATRIX}
    if not os.environ.get('NATIVE_CLI_ARCHIVES'):expected={host_archive().name}
    actual={p.name for p in Path('dist').iterdir()if p.name.endswith(('.tar.gz','.zip'))}
    assert actual==expected,'incomplete or unexpected native archives'
    notice=Path('dist/THIRD_PARTY_NOTICES.txt')
    assert notice.read_bytes()==Path('THIRD_PARTY_NOTICES.txt').read_bytes(),'starter notices changed'
    sums=''.join(hashlib.sha256((Path('dist')/name).read_bytes()).hexdigest()+'  '+name+'\n'for name in sorted(expected|{'THIRD_PARTY_NOTICES.txt'}))
    receipt=Path('dist/checksums.txt')
    if args.record:receipt.write_text(sums);return
    assert receipt.read_text()==sums,'release archive bytes changed'
    for name in sorted(expected):
        with tempfile.TemporaryDirectory()as directory:extract(Path('dist')/name,directory)
    with tempfile.TemporaryDirectory()as directory:
        binary=extract(host_archive(),directory)
        version=os.environ.get('VERSION','dev')
        revision=os.environ.get('COMMIT')or subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
        assert subprocess.check_output([binary,'version'],text=True,timeout=10).strip()==f'termcade {version}'
        identity=json.loads(subprocess.check_output([binary,'--build-info'],text=True,timeout=10))
        assert identity['version']==version and identity['commit']==revision,'source revision mismatch'
        if os.environ.get('NATIVE_CLI_ARCHIVES'):
            desired=next(target for system,arch,ext,target in MATRIX if host_archive().name==f'termcade_{system}_{arch}.{ext}')
            assert identity['target']==desired,'native target mismatch'
        fixture=Path(os.environ.get('CARGO_TARGET_DIR','target'))/'wasm32-wasip1/release/termcade_probe_fixture.wasm'
        subprocess.run(['python3','scripts/test_rust_cli.py','--binary',str(binary),'--wasm',str(fixture),'--expected-version',version],check=True)
    print(f'{len(expected)} native archives, exact checksums, source identity and CLI journeys verified')
if __name__=='__main__':main()
