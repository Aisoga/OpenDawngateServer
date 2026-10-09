"""Generate a stand-in for EA's "OTG3 Certificate Authority" and a redirector certificate signed by it.

The client trusts the CAs compiled into Dawngate.exe. re/scripts/patch_client.py writes the modulus
of the CA generated here over the OTG3 entry in a *working copy* of the exe, so certificates we sign
are accepted. Everything mirrors EA's real redirector certificate as seen in a capture:
RSA-1024 keys with public exponent 3, md5WithRSAEncryption, issuer = the OTG3 distinguished name.
(The `cryptography` package refuses MD5 signatures, so signing goes through Git's openssl CLI.)

This is a one-time setup tool for the Go server (server/, ADR-1): Go can't create e = 3 keys or sign
with MD5, so it stays in Python. The server reads server_cert.pem and server_key.pem from its --certs
folder (default server/certs).

Usage:
    .venv/Scripts/python -I server/tools/make_certs.py <out_dir> [server_common_name]
    e.g. .venv/Scripts/python -I server/tools/make_certs.py server/certs 127.0.0.1
Writes ca_key.pem, ca_cert.pem, ca_modulus.bin (128 bytes, big-endian), server_key.pem, server_cert.pem.
A new CA invalidates the patched working copy: re-run re/scripts/patch_client.py with the new
ca_modulus.bin afterwards. The keys are throwaway test keys; keep them out of git (server/certs/ is
ignored).
"""
import os
import subprocess
import sys

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa

OPENSSL = r"C:\Program Files\Git\usr\bin\openssl.exe"
SUBJ_OTG3 = ("/CN=OTG3 Certificate Authority/C=US/ST=California/L=Redwood City/O=Electronic Arts, Inc."
             "/OU=Online Technology Group/emailAddress=dirtysock-contact@ea.com")
SUBJ_SERVER = "/C=US/ST=California/O=Electronic Arts, Inc./OU=Global Online Studio/CN={cn}"
# Mirror EA's redirector certificate: PrintableString names (string_mask=nombstr; OpenSSL defaults to
# UTF8String) and the same v3 extensions. Whether ProtoSSL needs these is untested; the check that
# actually rejected our first certificates was the OTG3 "*.ea.com only" flag (see patch_client.py).
OPENSSL_CNF = """\
[req]
distinguished_name = dn
string_mask = nombstr
[dn]
[ca_ext]
basicConstraints = critical, CA:TRUE
subjectKeyIdentifier = hash
[server_ext]
basicConstraints = CA:FALSE
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid, issuer
[ca]
default_ca = local_ca
[local_ca]
dir = {dir}
database = $dir/index.txt
new_certs_dir = $dir
serial = $dir/serial.txt
default_md = md5
policy = any
unique_subject = no
[any]
countryName = optional
stateOrProvinceName = optional
localityName = optional
organizationName = optional
organizationalUnitName = optional
commonName = supplied
"""
# EA's redirector certificate is valid 2015-05-07 .. 2035-05-02. Copy that, which also keeps notAfter
# before 2038 in case the client stores certificate times in a 32-bit time_t (untested).
NOT_BEFORE, NOT_AFTER = "150507121318Z", "350502121318Z"


def write_key(path):
    key = rsa.generate_private_key(public_exponent=3, key_size=1024)
    with open(path, "wb") as f:
        f.write(key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.TraditionalOpenSSL,
                                  serialization.NoEncryption()))
    return key


def main(argv):
    if len(argv) not in (2, 3):
        sys.exit(__doc__)
    out = argv[1]
    cn = argv[2] if len(argv) == 3 else "127.0.0.1"
    os.makedirs(out, exist_ok=True)
    p = lambda name: os.path.join(out, name)  # noqa: E731

    def openssl(*args):
        subprocess.run([OPENSSL, *args], check=True, capture_output=True)

    with open(p("openssl.cnf"), "w") as f:
        f.write(OPENSSL_CNF.replace("{dir}", os.path.abspath(out).replace("\\", "/")))
    with open(p("index.txt"), "w"):
        pass
    with open(p("serial.txt"), "w") as f:
        f.write("03C2\n")
    ca_key = write_key(p("ca_key.pem"))
    write_key(p("server_key.pem"))
    openssl("req", "-config", p("openssl.cnf"), "-new", "-x509", "-md5", "-key", p("ca_key.pem"),
            "-subj", SUBJ_OTG3, "-extensions", "ca_ext", "-days", "7300", "-out", p("ca_cert.pem"))
    openssl("req", "-config", p("openssl.cnf"), "-new", "-md5", "-key", p("server_key.pem"),
            "-subj", SUBJ_SERVER.format(cn=cn), "-out", p("server.csr"))
    openssl("ca", "-batch", "-config", p("openssl.cnf"), "-preserveDN", "-notext", "-md", "md5",
            "-cert", p("ca_cert.pem"), "-keyfile", p("ca_key.pem"), "-in", p("server.csr"),
            "-startdate", NOT_BEFORE, "-enddate", NOT_AFTER, "-extfile", p("openssl.cnf"),
            "-extensions", "server_ext", "-out", p("server_cert.pem"))
    for leftover in os.listdir(out):  # openssl ca bookkeeping
        if leftover.startswith(("index.txt", "serial.txt")) or leftover.endswith((".csr", ".cnf")) \
                or (leftover.endswith(".pem") and leftover[:-4].isalnum() and leftover[:-4].isupper()):
            os.remove(p(leftover))
    with open(p("ca_modulus.bin"), "wb") as f:
        f.write(ca_key.public_key().public_numbers().n.to_bytes(128, "big"))
    print(f"wrote ca_key.pem, ca_cert.pem, ca_modulus.bin, server_key.pem, server_cert.pem to {out} (server CN={cn})")


if __name__ == "__main__":
    main(sys.argv)
