"""Package the native executable, preserving Termcade's five archive names."""
import json
import os
from pathlib import Path
import platform
import subprocess
import tarfile
import zipfile
from native_linkage import verify_linkage

system,arch,extension=(os.environ[name]for name in ('RELEASE_PLATFORM','RELEASE_ARCH','RELEASE_EXTENSION'))
assert platform.system()==system
assert platform.machine().lower() in ({'x86_64','amd64'}if arch=='x86_64'else{'arm64','aarch64'})
assert extension==('zip'if system=='Windows'else'tar.gz')
executable='termcade.exe'if system=='Windows'else'termcade'
target=os.environ.get('RUST_TARGET','')
binary=Path(os.environ.get('CARGO_TARGET_DIR','target'))/target/'release'/executable
assert subprocess.check_output([str(binary),'version'],text=True).strip()=='termcade '+os.environ['VERSION']
identity=json.loads(subprocess.check_output([str(binary),'--build-info'],text=True))
assert identity['version']==os.environ['VERSION'] and identity['commit']==os.environ['COMMIT']
if target:
    assert identity['target']==target
    verify_linkage(binary,system)
Path('dist').mkdir(exist_ok=True)
archive=Path('dist')/f'termcade_{system}_{arch}.{extension}'
if extension=='zip':
    with zipfile.ZipFile(archive,'w',compression=zipfile.ZIP_DEFLATED)as bundle:
        bundle.write(binary,executable)
else:
    with tarfile.open(archive,'w:gz')as bundle:
        bundle.add(binary,arcname=executable)
