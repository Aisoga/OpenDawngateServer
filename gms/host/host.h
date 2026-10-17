// Host mode, stage 1 (ADR-2 decision 2, roadmap D1d; docs/SERVER_MODE.md *Host mode, stage 1 (D1d)*).
//
// The DLL is in host mode when the process environment carries ADR-2's DGGMS_* variables (DGGMS_CONTROL,
// DGGMS_MATCH, DGGMS_TOKEN, DGGMS_BIND; any one non-empty is enough). Without them it stays the D1 harness. In host
// mode the client front end stays out of the way of the server simulator:
//   - boot hold: cBootState::Update (vtable 0x011DBE98 slot 10, 0x005425F0) is replaced by a no-op, so the game
//     never leaves kGameStateBoot: no login state, no Blaze, no lobby (the original counts 30 frames and then
//     changes to kGameStateLogin, or to kGameStateLobby when GetSporeOnline()->IsOffline());
//   - socket guard: the exe's own socket(), WSASocketA() and gethostbyname() fail (WSAENETDOWN / no host), so
//     nothing the front end starts at app init (telemetry rules, DirtySDK, QoS) opens a socket or resolves a name.
//     Only Dawngate.exe's import table is hooked: the GMS endpoint that G2 links into this DLL uses the DLL's own
//     WS2_32 imports and is not affected;
//   - hidden window: top-level windows are created without WS_VISIBLE and never shown, activated or brought to the
//     foreground; low-level keyboard/mouse hooks (the game's WH_KEYBOARD_LL at 0x00969BB7, which swallows Alt+Tab
//     and Ctrl+Esc) are refused; message boxes are logged and answered without showing;
//   - folders: SHGetFolderPathW for the per-user folders (AppData, LocalAppData, Documents, ...) points into the
//     host's data folder (DGGMS_DATA, else <game dir>\host\<match>), so Application.log, Preferences.prop and the
//     portrait cache never touch the player's %APPDATA%\DawngateData or Documents\Dawngate.
// Each piece can be switched off in dgsim.ini [host] (hold_boot, block_sockets, hide_window, redirect_folders).
// Since G2 the host also serves the GMS port itself (gms_host.h): RakNet 4.081 bound to DGGMS_BIND (default
// 127.0.0.1:10042; "off" = no endpoint, for runs where another process serves the GMS).
// A second instance next to a client needs Dawngate.exe's -multipleInstances switch (main 0x006B2E20 creates the
// mutex Global\Dawngate otherwise) and the launcher event Global\Dawngate L2G. The starter gives the switch
// (run_session.ps1 -HostDir, dgserver C1); since H4a the host creates the event itself at DllMain time when nobody
// holds it (under Wine the starter can't: [host] launcher_event, default 1).
#pragma once

#include <cstddef>

struct HostConfig {
  bool enabled = false;
  char match[64] = "";     // DGGMS_MATCH
  char control[64] = "";   // DGGMS_CONTROL (host:port of dgserver's control listener, C1)
  char token[128] = "";    // DGGMS_TOKEN
  char bind[64] = "";      // DGGMS_BIND (the GMS endpoint's address, G2)
  char dataDir[260] = "";  // DGGMS_DATA, else <game dir>\host\<match>
  char options[4096] = "";  // DGGMS_OPTIONS: "key=value;..." overriding dgsim.ini [host] keys (G2; development runs)
};

// Reads the environment (DllMain, before the log is opened: host mode logs into <data dir>\dgsim.log).
// Creates the data folder in host mode.
const HostConfig& HostDetect(const char* gameDir);
const HostConfig& HostGet();

// Installs the host-mode hooks (DllMain time, before the game's main()). `ini` is <game dir>\dgsim.ini.
void HostInstall(const char* ini);

// A [host] setting: DGGMS_OPTIONS ("key=value;key=value", e.g. run_session.ps1 -HostOptions) first, then
// dgsim.ini [host], then `def`. Only after HostInstall (it records the ini path).
int HostOptionInt(const char* key, int def);
void HostOptionString(const char* key, const char* def, char* out, std::size_t size);

// Startup milestones: logs the time since process creation together with memory and CPU.
void HostMark(const char* what);

// One line: private bytes, working set (peak), address space used, CPU time, refused sockets, boot holds.
void HostFormatStats(char* buf, std::size_t size);
