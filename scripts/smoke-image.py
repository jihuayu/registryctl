#!/usr/bin/env python3
"""生成独立、可原生拉取的 scratch OCI 镜像，仅用于人工触发 CI 验收。"""
import hashlib,io,json,pathlib,sys,tarfile
root=pathlib.Path(sys.argv[1]);blobs=root/'blobs/sha256';blobs.mkdir(parents=True,exist_ok=True)
def write(data,media):
    value=hashlib.sha256(data).hexdigest();(blobs/value).write_bytes(data)
    return {'mediaType':media,'digest':'sha256:'+value,'size':len(data)}
layer=io.BytesIO()
with tarfile.open(fileobj=layer,mode='w') as tar:
    data=b'registryctl GitHub Actions acceptance\n';entry=tarfile.TarInfo('registryctl-acceptance.txt');entry.size=len(data);entry.mode=0o644;tar.addfile(entry,io.BytesIO(data))
layer_data=layer.getvalue();layer_desc=write(layer_data,'application/vnd.oci.image.layer.v1.tar')
config=write(json.dumps({'architecture':'amd64','os':'linux','config':{},'rootfs':{'type':'layers','diff_ids':[layer_desc['digest']]}}).encode(),'application/vnd.oci.image.config.v1+json')
manifest=write(json.dumps({'schemaVersion':2,'mediaType':'application/vnd.oci.image.manifest.v1+json','config':config,'layers':[layer_desc]}).encode(),'application/vnd.oci.image.manifest.v1+json')
(root/'index.json').write_text(json.dumps({'schemaVersion':2,'mediaType':'application/vnd.oci.image.index.v1+json','manifests':[manifest]}))
(root/'oci-layout').write_text('{"imageLayoutVersion":"1.0.0"}')
