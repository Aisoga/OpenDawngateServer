OpenDawngateServer: the Linux server (start-server)          (release @VERSION@)
==============================================================================

An unofficial fan project to play the 2014 MOBA Dawngate again. It is not affiliated with or endorsed by
Electronic Arts or Waystone Games; Dawngate is a trademark of its owners. This release contains no game file:
every server and every player needs their own Dawngate.zip from archive.org.

This archive is the server for a Linux machine. Players play on Windows with Start-Client.exe from the Windows
release of the same version. The server's match hosts run the game under Wine.

A WORD ON SAFETY. The game is 2014 code. A public server gets checked passwords, an encrypted tunnel, limits
and a website for accounts, and is still run at the operator's own risk.


What you need
-------------
- A 64-bit Linux machine with Wine and its 32-bit part (Debian 12 and Ubuntu 24.04 work). On Debian or Ubuntu:
    sudo dpkg --add-architecture i386
    sudo apt update
    sudo apt install --no-install-recommends wine wine32 wine64
- Dawngate.zip (1.3 GB) from https://archive.org/details/Dawngate
- About 3 GB of free disk space (the match hosts' copy of the game and their Wine prefix), plus the zip.
No graphical desktop, no X server.


Setting it up
-------------
1. A user and folder for the server:
     sudo useradd --system --create-home --home-dir /srv/dawngate --shell /usr/sbin/nologin dawngate
2. Unpack this archive into it, put Dawngate.zip next to start-server, give the folder to the user:
     sudo tar xzf <this archive> -C /srv/dawngate --strip-components=1
     sudo cp Dawngate.zip /srv/dawngate/
     sudo chown -R dawngate:dawngate /srv/dawngate
3. The first start, in a terminal:
     cd /srv/dawngate
     sudo -u dawngate ./start-server
   It asks who will play (2: your home network, 3: the internet), writes Start-Server.ini, checks Wine, makes
   the match hosts' Wine prefix (Dawngate-Data/wine, about half a minute), sets up Dawngate-Data and starts the
   server. It shows the server's key fingerprint for your players. Ctrl+C stops it.
4. As a service from then on (dawngate.service is in this archive; it expects the folder /srv/dawngate):
     sudo cp /srv/dawngate/dawngate.service /etc/systemd/system/
     sudo systemctl daemon-reload
     sudo systemctl enable --now dawngate
   journalctl -u dawngate -f shows its output; sudo systemctl stop dawngate stops it and its matches.
Without Wine's 32-bit part start-server says what to install and starts nothing.
./start-server --check reports what is missing; --configure asks the first start's questions again.


Ports
-----
Players need TCP and UDP 42128 (the encrypted tunnel) and TCP 443 (the website). Let only these (and SSH) in:
    sudo ufw default deny incoming ; sudo ufw allow OpenSSH ; sudo ufw allow 42128 ; sudo ufw allow 443/tcp
    sudo ufw enable
If a web server such as nginx already holds port 443 on this machine, let it serve the website: web_proxy = on
in Start-Server.ini (docs/hosting.md, "The website behind nginx").
Never open TCP 10040 (the match hosts' control channel, no login) or UDP 10042-10051 (the matches, which
listen on 127.0.0.1 and are reached through the tunnel).


Players
-------
Each player unpacks the Windows release of the same version with their own Dawngate.zip and adds your address
to the server list on the game's login screen (or starts Start-Client.exe <address>). Their first login shows
your server's key fingerprint: they compare it with the one start-server shows, then log in again.


Accounts, updates, backups
--------------------------
Account commands work while the server runs, as the server's user, e.g.
    sudo -u dawngate ./start-server --create-account alex
    sudo -u dawngate ./start-server --make-invite --invite-uses 5
To update: stop the service, unpack the new archive over the folder as in step 2 (then chown again), start it.
Start-Server.ini and Dawngate-Data stay. Back up Dawngate-Data/data (the accounts) and Dawngate-Data/certs
(the server's keys) with the server stopped.

The whole guide: docs/hosting.md, "Linux server", in the project's source.


Licence
-------
This release is free software under the GNU Affero General Public License v3 (LICENSE.txt). Third-party
notices: THIRD-PARTY-NOTICES.txt. Its source is the commit named in BUILD.txt.
