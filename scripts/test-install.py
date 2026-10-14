#!/usr/bin/env python3
"""用本地发布包验证安装和篡改拒绝，不访问真实下载源或覆盖用户程序。"""
import os,pathlib,platform,shutil,subprocess,sys,tempfile
sys.stdout.reconfigure(encoding='utf-8')
sys.stderr.reconfigure(encoding='utf-8')
root=pathlib.Path(__file__).resolve().parents[1]
release=(root/'VERSION').read_text().strip()
number=release.removeprefix('v')
system={'Windows':'windows','Darwin':'darwin','Linux':'linux'}[platform.system()]
arch={'AMD64':'amd64','x86_64':'amd64','aarch64':'arm64','arm64':'arm64'}[platform.machine()]
shell=os.environ.get('REGISTRYCTL_TEST_SHELL','sh')
subprocess.run(['python','scripts/package.py','--version',release,'--platform',system+'/'+arch],cwd=root,check=True)
with tempfile.TemporaryDirectory(prefix='registryctl-install-') as tmp:
    temp=pathlib.Path(tmp);fake=temp/'fake';fake.mkdir();downloads=temp/'downloads';downloads.mkdir()
    for file in (root/'dist').glob('*'):
        if file.is_file():shutil.copy2(file,downloads/file.name)
    curl=fake/'curl'
    curl.write_text('''#!/bin/sh
set -eu
url='';target=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    https://github.com/jihuayu/registryctl/releases/download/v*/*) url=$1; shift ;;
    -o) target=$2; shift 2 ;;
    *) shift ;;
  esac
done
[ -n "$url" ] && [ -n "$target" ]
cp "$TEST_DOWNLOADS/${url##*/}" "$target"
''',encoding='utf-8');curl.chmod(0o755)
    install=temp/'installed'
    fake_bin=fake.as_posix()
    if os.name=='nt':fake_bin='/'+fake_bin[0].lower()+fake_bin[2:]
    env=dict(os.environ,TEST_FAKE_BIN=fake_bin,TEST_DOWNLOADS=downloads.as_posix(),TEST_INSTALL_SCRIPT=(root/'install.sh').as_posix(),TEST_INSTALL_DIR=install.as_posix())
    command=[shell,'-c','PATH="$TEST_FAKE_BIN:$PATH"; export PATH; exec sh "$TEST_INSTALL_SCRIPT" --bin-dir "$TEST_INSTALL_DIR"']
    subprocess.run(command,env=env,check=True)
    binary=install/('registryctl.exe' if system=='windows' else 'registryctl')
    version=subprocess.check_output([str(binary),'version'],text=True)
    assert 'registryctl '+release in version
    before=binary.read_bytes()
    (downloads/'SHA256SUMS').write_text('0'*64+'  registryctl_'+number+'_'+system+'_'+arch+('.zip' if system=='windows' else '.tar.gz')+'\n')
    refused=subprocess.run(command,env=env,capture_output=True)
    assert refused.returncode!=0 and binary.read_bytes()==before,'篡改包被安装或覆盖现有程序'
print('正常安装、嵌入版本和篡改校验拒绝通过')
