// gms/core/agents (BOTBUSH): what a bot may know of an enemy shaper. Engine-free; the host (gms/engine/agents.cpp)
// measures who is an enemy shaper and whether the bot sees it (Agent_IsVisibleTo 0x008A9B90 with the bot's creature as
// the viewer: a shaper noun has no NonPlayerClass, so its vision rule is "everything" and the test reads the host's
// mVisibleToTeam, which hides fog, grass and stealth), the Lua side (gms/engine/lua/dgsim_bots_api.lua B.SeenBy) the
// same through nGameObject.IsVisibleToTeam.
//
// Rule (the user's request 2026-10-24: "bots can see what's in bushes"): a bot never aims at, chases by its position,
// counts or flees from an enemy shaper its team doesn't see, as a human player can't; what its team saw a moment ago it
// may still use where a script asks for that history (nBot.GetNumPlayersNearPosition's historySec), and an attack from
// the grass reveals the attacker by the game's own rule (AgentBlackboard_RevealInGrass, the host's fog). Everything that
// is not an enemy shaper (minions, creeps, structures, allies) is left as it was. [host] bot_fog_check=0 restores the
// old behaviour.
#pragma once

namespace gms {
namespace agents {

// Whether a bot may use `target` at all: anything but an enemy shaper, or an enemy shaper its team sees.
inline bool BotMayKnow(bool fogCheck, bool enemyShaper, bool seen) { return !fogCheck || !enemyShaper || seen; }

// A gambit whose conditions passed, aimed at an object (its condition's target, else the bot's mTarget): blocked when
// the object is an enemy shaper the bot doesn't see and the gambit is one of the shaper's own abilities (basic attack,
// Q, W, E, R, a spell: no ability GUID of its own). The bots' own abilities (a GUID: ability_Bot_FollowTarget,
// _MoveToTarget, _Back, ...) still run: their walk to an unseen object goes to where the team last saw it (the
// engine's Locomotion_FollowObject, D2g-pursuit), as a human walks to the bush the enemy vanished into. Items (potions,
// wards) are never blocked; a gambit aimed at a point alone has no object (`hasObject` false).
inline bool SightBlocksGambit(bool fogCheck, bool hasObject, bool consumable, bool botAbility, bool enemyShaper,
                              bool seen) {
  if (!fogCheck || !hasObject || consumable || botAbility) return false;
  return !BotMayKnow(fogCheck, enemyShaper, seen);
}

}  // namespace agents
}  // namespace gms
