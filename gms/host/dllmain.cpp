// dgsim.dll: the D1 server-mode harness, loaded by Dawngate.exe as PhysXLoader.dll from the
// server working copy (see forward.cpp and re/scripts/setup_server_copy.py). With ADR-2's DGGMS_*
// environment it runs in host mode (stage 1, D1d: host.h); without it, it is the D1 harness.
#include <windows.h>

#include <cstring>

#include "engine/experiment.h"
#include "engine/log.h"
#include "host.h"

BOOL WINAPI DllMain(HINSTANCE self, DWORD reason, LPVOID) {
  if (reason != DLL_PROCESS_ATTACH) return TRUE;
  DisableThreadLibraryCalls(self);
  // Static imports are initialised on the process's primary thread, which runs the game loop.
  char dir[MAX_PATH];
  GetModuleFileNameA(nullptr, dir, MAX_PATH);
  if (char* slash = std::strrchr(dir, '\\')) *slash = 0;
  // Host mode logs into its data folder (one per match); the harness into the game folder.
  const HostConfig& host = HostDetect(dir);
  char logPath[MAX_PATH];
  wsprintfA(logPath, "%s\\dgsim.log", host.enabled ? host.dataDir : dir);
  LogOpen(logPath);
  Log("dgsim.dll loaded (pid %lu, primary thread %lu), game dir %s, %s", GetCurrentProcessId(), GetCurrentThreadId(), dir,
      host.enabled ? "host mode" : "harness mode (no DGGMS_* environment)");
  char ini[MAX_PATH];
  wsprintfA(ini, "%s\\dgsim.ini", dir);
  HostInstall(ini);
  ExperimentInstall(dir, GetCurrentThreadId());
  return TRUE;
}
