import json, pathlib, subprocess, sys, tempfile
BIN, ROOT = sys.argv[1], pathlib.Path(sys.argv[2])
M = "github.com/kcl1104/geminus"
CGO = "package zzproxy\n\n/*\n#include <sys/socket.h>\n*/\nimport \"C\"\n\nfunc ZZSock() int { return int(C.socket(2, 1, 0)) }\n"
STUB = "//go:build !cgo\n\npackage zzproxy\n\nfunc ZZSock() int { return -1 }\n"
for name, user in [("pack-via-proxy-cgo-with-stub", "internal/domainpack/softwaredelivery/zz_probe.go"), ("kernel-via-proxy-cgo-with-stub", "internal/simulation/engine/zz_probe.go")]:
    pkg = pathlib.Path(user).parent.name
    files = {"internal/zzproxy/p.go": CGO, "internal/zzproxy/p_nocgo.go": STUB, user: f"package {pkg}\n\nimport \"{M}/internal/zzproxy\"\n\nvar _ = zzproxy.ZZSock\n"}
    with tempfile.TemporaryDirectory() as d:
        rep = {}
        for i, (p, src) in enumerate(files.items()):
            f = pathlib.Path(d) / f"f{i}.go"; f.write_text(src); rep[str(ROOT / p)] = str(f)
        ov = pathlib.Path(d) / "ov.json"; ov.write_text(json.dumps({"Replace": rep}))
        r = subprocess.run([BIN, "-C", str(ROOT), "-overlay", str(ov)], capture_output=True, text=True)
        out = (r.stdout + r.stderr).strip().replace("\n", " | ")
        print(f"{name}\texit={r.returncode}\t{'OK' if r.returncode == 1 else 'MISSED'}\t{out[:300]}", flush=True)
