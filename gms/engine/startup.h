// The headless host's start-up, stage 2 (roadmap H1-live; ADR-2 decision 2; docs/SERVER_MODE.md *Headless host,
// stage 2 (H1-live)*, planned in *Headless stage 2 plan (H1-static)*).
//
// Everything here patches Dawngate.exe's start-up by address (game.h, namespace startup). host/headless.cpp reads the
// [host] keys and calls Install from HostInstall (DllMain time, before the game's main), so it applies in host mode
// only, and each piece only when its key is on (all off by default):
//   marks     (step 0, startup_marks): about 20 call-site probes in SporeApp_Initialize, Battlegrounds_AppInit,
//             GameClient_Init and GameGraphics_Initialize log "before"/"after" marks (time, memory) through the
//             host's HostMark, so the start-up's phases split into numbers;
//   switches  (step 1, nobake / nosound): CommandLine_FindSwitch answers the shipped switches -nobake (no portrait
//             bake: no Shaper_Gallery level, no portraits.snap) and -noSound (no Wwise, no banks) as given;
//   preload   (step 2, preload = 1 all / 0 none / 2 server): the preloader's directory callback is a filter that skips
//             every directory of script/preload.txt (0) or keeps only assetdata/binary and levels (2);
//   core_only (step 3): SporeApp_Initialize jumps over the D3D9 device (1: from 0x006B26C8) or only over SP_Graphics
//             (2: from 0x006B279E, H1-static's point), audio, UI and the AppInit message (the whole client game) to
//             its own success exit 0x006B2D93, after CoreInit has run the simulation half of Battlegrounds_AppInit
//             and the GMS message registration and set the x87 FPU as a D3D9 device would. SP_App +0xC stays 0, so
//             MainLoop_RunFrame does nothing; the shipped main loop still pumps the window manager, whose PeekMessage
//             drives the harness, and CoreFrame stands in for the per-frame engine work the server needs (the
//             loader, the message dispatch, and the Gears module's game clock, NavPower frame and update list).
//   d3d9      (H1d, with core_only 1 and d3d9 = 0, the default): no Direct3D 9 at all. The render manager's call of
//             RwDevice_Initialize (0x00997195) gets the host's answer (no Direct3DCreate9; g_RwD3D9 = a stand-in that
//             fails and logs every call, gms/core/d3d9/stand_in.h), the config manager's adapter-mode enumeration
//             (0x0096381B) is skipped. A Direct3DCreate9 watch (IAT) counts every call in every host; test_no_d3d9
//             makes it answer NULL (a machine without D3D9).
#pragma once

namespace startup {

struct Options {
  bool marks = false;
  bool nobake = false;
  bool nosound = false;
  int preload = 1;  // 1 = the game's own preloader, 0 = nothing, 2 = only assetdata/binary and levels
  int coreOnly = 0;  // 0 = off, 1 = jump before the D3D9 device (0x006B26C8), 2 = before SP_Graphics (0x006B279E)
  // H1d: 0 = with core_only 1, no D3D9 at all (RwDevice_Initialize answered by the host, no Direct3DCreate9); 1 = the
  // game's own RwDevice_Initialize (an IDirect3D9 object at start-up, as before H1d). Stage 1 and core_only 2 need it.
  int d3d9 = 0;
  // H1d test aid: Direct3DCreate9 answers NULL, as on a machine without Direct3D 9 (with d3d9 = 1 or core_only 0/2
  // the host then can't start: "Could not start the renderer", the shipped failure).
  bool testNoD3d9 = false;
};

using MarkFn = void (*)(const char* what);

// Installs what `o` asks for (each piece checks the bytes it patches first and logs what it did). `mark` is the
// host's HostMark (time since process start, memory, CPU).
void Install(const Options& o, MarkFn mark);

// The core-only jump is installed (step 3).
bool CoreOnly();
// CoreInit ran every step without an exception (false before it ran).
bool CoreInitOk();
// Core-only mode, once per engine frame (about 30 Hz, before the harness's frame): the loader's update, the message
// manager's dispatch, the game clock, NavPower's frame and the update list (with the frame's dt in seconds): the
// server-relevant part of MainLoop_RunFrame and of the Gears module's frame update. Each piece is a guards.h step
// (H5): an exception skips that frame's run of it; one that throws in every frame for [host] step_fail_s ends the
// match (the harness switches it off).
void CoreFrame(float dtSeconds);
// D3D9 at start-up (H1d): "not touched" when nothing called Direct3DCreate9 or the stand-in (gms::d3d9::Verdict).
const char* D3d9Verdict();
// "probes 20 (40 marks), switches nobake noSound (answered 2), preload 0 (19 skipped, 0 loaded), core only ok"
void Describe(char* out, unsigned size);

}  // namespace startup
