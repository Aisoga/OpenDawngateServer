"""Extract a client zip, keeping the original file timestamps stored in the archive.

Usage: python -I re/scripts/extract_client.py <archive.zip> <dest_dir>
(re/scripts/setup_all.py does this step too, into a temporary folder that is renamed once every file has been checked
against docs/file_manifest.csv.)
"""
import os
import sys
import time
import zipfile


def extract(src, dst, members=None):
    """Extract `src` (or only the ZipInfo entries in `members`) into `dst` with the archive's mtimes; returns the count."""
    os.makedirs(dst, exist_ok=True)
    with zipfile.ZipFile(src) as z:
        infos = z.infolist() if members is None else members
        for i in infos:
            z.extract(i, dst)  # extract() sanitizes absolute and ".." paths
            if not i.is_dir():
                t = time.mktime(i.date_time + (0, 0, -1))
                os.utime(os.path.join(dst, i.filename), (t, t))
    return len(infos)


if __name__ == "__main__":
    src, dst = sys.argv[1], sys.argv[2]
    print("extracted", extract(src, dst), "entries from", src)
