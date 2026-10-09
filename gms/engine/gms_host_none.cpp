// dgsim.dll built without RakNet (gms/CMakeLists.txt without tools/raknet): host mode runs without a GMS endpoint.
#include "gms_host.h"

#include <cstdio>

#include "log.h"

bool GmsHostStart(void*, void*, const char*, unsigned, unsigned) {
  Log("host: dgsim.dll was built without RakNet (gms/CMakeLists.txt); no GMS endpoint");
  return false;
}
void GmsHostPoll() {}
bool GmsHostActive() { return false; }
unsigned GmsHostPort() { return 0; }
int GmsHostConnections() { return 0; }
void GmsHostShutdown() {}
void GmsHostFormatStats(char* buf, std::size_t size) { std::snprintf(buf, size, "no endpoint (built without RakNet)"); }
void GmsHostTickCommands() {}
void GmsHostFlush() {}
void GmsHostTickNetworkRate() {}
bool GmsHostFormatCommandStats(char*, std::size_t) { return false; }

// D6a: the bot players need the replication (engine/replicate.cpp) and gms_core, so without RakNet there are none.
#include "bots.h"
namespace bots {
void Configure(const Options&) {}
void OnSimulatorCreated(void*) {}
void OnLevelStarted(void*) {}
const std::vector<Bot>& EnsurePlayers(void*, const std::vector<Human>&, const std::vector<Human>&) { return Players(); }
const std::vector<Bot>& Players() {
  static const std::vector<Bot> kNone;
  return kNone;
}
bool IsBotIndex(int) { return false; }
int DeployAll(void*) { return 0; }
void Tick(void*) {}
void FormatStats(char* buf, std::size_t size) { std::snprintf(buf, size, "no bots (built without RakNet)"); }
}  // namespace bots

// D11b: the banter lines go out through the command intake's feedback sender (engine/commands.cpp), so without RakNet
// there are none.
#include "banter.h"
namespace banter {
void Install(const Options&) {}
bool Enabled() { return false; }
void Tick(void*, double) {}
void OnPlayerKill(void*, int, int, const std::vector<gms::banter::Assist>&, unsigned) {}
void OnPlayerLeft(void*, int) {}
void OnPlayerReconnected(void*, int) {}
unsigned EmoteVariant(void*, unsigned, std::uint32_t, const char*, unsigned randomVariant) { return randomVariant; }
void FormatStats(char* buf, std::size_t size) { std::snprintf(buf, size, "no banter (built without RakNet)"); }
}  // namespace banter
