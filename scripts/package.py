#!/usr/bin/env python3
"""从准确提交交叉编译并打包客户端，保留版本、校验值与许可证。"""
import argparse, datetime, hashlib, json, os, pathlib, re, subprocess, tarfile, zipfile

root = pathlib.Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser()
p.add_argument('--version', required=True)
p.add_argument('--platform', action='append', choices=['linux/amd64','linux/arm64','darwin/amd64','darwin/arm64','windows/amd64','windows/arm64'])
a = p.parse_args()
if not re.fullmatch(r'v\d+\.\d+\.\d+', a.version):
    raise SystemExit('必须指定完整版本 vMAJOR.MINOR.PATCH')
revision = subprocess.check_output(['git','rev-parse','HEAD'], cwd=root, text=True).strip()
date = datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')
dist = root/'dist'; dist.mkdir(exist_ok=True)
license_dir = dist/'licenses'; license_dir.mkdir(exist_ok=True)
subprocess.run(['go','mod','download','all'],cwd=root,check=True)
modules = subprocess.check_output(['go','list','-m','-json','all'], cwd=root, text=True)
decoder = json.JSONDecoder()
while modules.strip():
    module, end = decoder.raw_decode(modules.lstrip()); modules = modules.lstrip()[end:]
    if module.get('Main'): continue
    source = pathlib.Path(module['Dir'])
    found = next((source/name for name in ['LICENSE','LICENSE.txt','LICENSE.md','COPYING'] if (source/name).is_file()), None)
    if found is None: raise SystemExit('缺少依赖许可证：'+module['Path'])
    name = module['Path'].replace('/','_')+'@'+module['Version']+'.txt'
    (license_dir/name).write_bytes(found.read_bytes())
goroot = pathlib.Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
(license_dir/'Go-BSD-3-Clause.txt').write_bytes((goroot/'LICENSE').read_bytes())
entries = []
platforms = a.platform or ['linux/amd64','linux/arm64','darwin/amd64','darwin/arm64','windows/amd64','windows/arm64']
for platform in platforms:
    target_os, target_arch = platform.split('/')
    binary_name = 'registryctl.exe' if target_os=='windows' else 'registryctl'
    binary = dist/(target_os+'-'+target_arch)/binary_name
    binary.parent.mkdir(exist_ok=True)
    env = dict(os.environ, CGO_ENABLED='0', GOOS=target_os, GOARCH=target_arch)
    flags = f'-s -w -X main.version={a.version} -X main.commit={revision} -X main.buildDate={date}'
    subprocess.run(['go','build','-trimpath','-ldflags',flags,'-o',str(binary),'./cmd/registryctl'],cwd=root,env=env,check=True)
    files = [(binary,binary_name)] + [(root/name,name) for name in ['LICENSE','README.md','THIRD_PARTY.md','source.json']]
    files += [(file,'licenses/'+file.name) for file in sorted(license_dir.iterdir())]
    base = f'registryctl_{a.version[1:]}_{target_os}_{target_arch}'
    if target_os=='windows':
        archive = dist/(base+'.zip')
        with zipfile.ZipFile(archive,'w',compression=zipfile.ZIP_DEFLATED) as output:
            for file, name in files: output.write(file,name)
    else:
        archive = dist/(base+'.tar.gz')
        with tarfile.open(archive,'w:gz') as output:
            for file, name in files:
                info = output.gettarinfo(str(file),arcname=name)
                info.uid=info.gid=0;info.uname=info.gname='';info.mode=0o755 if name==binary_name else 0o644
                with file.open('rb') as data:output.addfile(info,data)
    entries.append((archive.name, hashlib.file_digest(archive.open('rb'),'sha256').hexdigest()))
(dist/'SHA256SUMS').write_text(''.join(digest+'  '+name+'\n' for name,digest in entries),encoding='utf-8')
(dist/'release.json').write_text(json.dumps({'version':a.version,'revision':revision,'go':'1.26.8','builtAt':date,'platforms':platforms},indent=2)+'\n',encoding='utf-8')
print('已打包 '+str(len(platforms))+' 个平台，版本 '+a.version+'，提交 '+revision)
