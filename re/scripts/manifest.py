"""Write a CSV manifest (path, size, sha256, crc32, mtime) comparing two extracted client builds.

Usage: python -I re/scripts/manifest.py <buildA_root> <buildB_root> <out.csv>
"""
import csv, datetime, hashlib, os, sys, zlib


def scan(root):
    out = {}
    for dirpath, _, files in os.walk(root):
        for name in files:
            full = os.path.join(dirpath, name)
            rel = os.path.relpath(full, root).replace(os.sep, "/")
            h = hashlib.sha256(); crc = 0
            with open(full, "rb") as f:
                for chunk in iter(lambda: f.read(1 << 20), b""):
                    h.update(chunk); crc = zlib.crc32(chunk, crc)
            st = os.stat(full)
            out[rel] = (st.st_size, h.hexdigest(), f"{crc:08x}",
                        datetime.datetime.fromtimestamp(st.st_mtime).strftime("%Y-%m-%d %H:%M:%S"))
    return out


a, b, dst = sys.argv[1:4]
A, B = scan(a), scan(b)
with open(dst, "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["path", "status", "size_A", "sha256_A", "crc32_A", "mtime_A", "size_B", "sha256_B", "crc32_B"])
    for rel in sorted(set(A) | set(B)):
        x, y = A.get(rel), B.get(rel)
        status = "only_A" if not y else "only_B" if not x else "identical" if x[1] == y[1] else "different"
        w.writerow([rel, status, *(x or ("",) * 4), *((y[:3]) if y else ("",) * 3)])
print("wrote", dst, len(set(A) | set(B)), "paths")
