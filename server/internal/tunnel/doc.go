// Package tunnel is the encrypted tunnel between Start-Client.exe and a public server (
// ). The 2014 client speaks TLS 1.1 with RSA-1024/MD5 to the redirector, plain TCP to Blaze and plain
// UDP (RakNet, QoS) to the match hosts, and none of that can be patched into something safe; the tunnel goes around
// it. The player copy is patched for a loopback address (the local profile, normally 127.0.0.1) and Start-Client runs
// the client side of the tunnel there: every port the game uses (TCP 42127, 10041, 17502, 18080, 9988; UDP 17502 and
// the match hosts' 10042-10051) listens on that address and carries the game's traffic to one gateway port of the
// server (default 42128, TCP and UDP).
//
// The wire:
//
//   - TCP: every connection the game opens becomes its own TLS 1.3 connection to the gateway. The gateway's key is a
//     self-signed ECDSA P-256 certificate that the client pins by the SHA-256 of its SubjectPublicKeyInfo (trust on
//     first use, as the redirector CA's; Fingerprint is what a player compares). The first bytes are a hello (wire.go): a
//     control session, or a stream to one of the server's ports that names its session. The game's TLS 1.1 to the
//     redirector ends in Start-Client itself, with a certificate set of the player's own PC (CN = the tunnel
//     address), so one patched copy serves every tunnelled server and the old TLS never crosses the network; the
//     redirector's request then travels inside the stream's TLS 1.3.
//   - UDP: datagrams go to the gateway's UDP port in an AES-256-GCM channel (channel.go) whose two keys (one per
//     direction) come from the control session's TLS connection (ExportKeyingMaterial), with a 64-bit counter as the
//     nonce and a replay window. Each datagram carries the service port and the game socket's own address, so the
//     server can answer the right socket, and its QoS check sees the client's address as a local client's (NAT type
//     OPEN, B2).
//   - The gateway (gateway.go, in dgserver) hands streams to the server's services in process (virtual listeners:
//     the services see the client's own address and the tunnel address, Addr), answers QoS datagrams in process and
//     forwards the match hosts' datagrams over the server machine's own UDP sockets. Blaze tells a tunnelled client
//     the tunnel address in every address it hands out (HNET, ping site, telemetry), as for a local client.
//
// Stdlib only (crypto/tls, crypto/aes, crypto/cipher, crypto/ecdsa).
package tunnel
