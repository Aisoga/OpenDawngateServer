// The D1 experiment: a second cGameSimulator in server mode, driven from the game's main thread.
#pragma once

// Called once from DllMain (PROCESS_ATTACH): hooks only, no game calls.
void ExperimentInstall(const char* gameDir, unsigned long mainThreadId);
