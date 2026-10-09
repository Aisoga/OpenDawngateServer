-- D14 live probe: the human's death to each kind of NPC killer (docs/MATCH_FLOW.md *D14*; DAMAGE.md D1h's recap).
-- [host] test_lua=<s>:dgsim_d14_deaths.lua at five times at least 30 s apart, from match 95 s (the camps exist from 90 s);
-- the human may stay in its base. Each call kills the human with one lethal true hit from the killer's own snapshot,
-- through the host's damage core, so the killing blow's CombatEvent carries the killer as sourceID and the client's
-- CombatEvent_KillingBlow picks its announcer line from the killer's NPC type [script]:
--   1 the nearest Creep_Jungle_* (NPC type 3, or 7 for a buff camp's major): "A Minion has slain ..." (MinionKilledYou);
--     the recap's source: the creep's noun, NPC type 3/7, team 0 (DeathRecapUI labels 3/6/7 as creeps);
--   2 the enemy Guardian (GetGuardianOnTeam; NPC type 13): "The Guardian has slain ..." (TitanKilledYou), recap type 13;
--   3 the parasite (GetParasiteID; NPC type 8, also as an egg): "A Minion has slain ..." (type 8 isn't Guardian, Tower or
--     Locus in the script), recap type 8 (labelled and iconed as an objective);
--   4 the nearest enemy Binding (NPC type 9): "A Binding has slain ..." (TowerKilledYou), recap type 9;
--   5 an enemy bot's creature hits the human for 10 true, then the nearest creep's lethal hit in the same call: the
--     victim's last player to hit is the bot (DamageTracker_GetLastPlayerToHit 0x008E1020), so the killing blow's
--     lastPlayerToHitID is the bot's creature: "<bot> has slain <you>" (PlayerKilledYou), the kill and its gold to the bot
--     (the host's `death:` line "killer player N"), the recap with both sources.
-- In every case: no gold or XP to anyone from the NPC kill itself ("Player" reward data 0/0), the death timer and the
-- respawn (D1g). A call while the human is dead does nothing but report (retry the part later).
-- Check in dgsim.log: the `death:` line (victim, killer noun and NPC type), `DeathRecap:` (sources, NPC types), the
-- rewards; on the client: the announcer text and the recap screen (screenshots 1-3 s after each part with
-- -ShotFocus on), `application_log.txt` free of DeathRecapUI.cpp asserts.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

-- nPlayer.IsBot takes the player's creature (nPlayer_IsBot 0x00947660 -> SporeLabsLua_GetObjectArg), not its index
local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c, pid end
  end
end

local function nearest(h, test)
  local best, bd = nil, 1e9
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if not seen[tostring(id)] then
      seen[tostring(id)] = true
      local name = nGameObject.GetAssetName(id) or ''
      if test(id, name) and nGameObject.IsAlive(id) then
        local d = nGameObject.GetObjectDistance(id, h)
        if d and d < bd then best, bd = id, d end
      end
    end
  end
  return best
end

local function describe(o)
  if not o then return 'none' end
  return string.format('%s %s (NPC type %s, team %s)', tostring(o), tostring(nGameObject.GetAssetName(o)),
    tostring(nGameObject.GetNPCType(o)), tostring(nGameObject.GetTeam(o)))
end

local function hitFrom(src, h, amount)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(src, kObjIDNone)
  local s, dealt = nGameObject.TakeDamage(snap, h, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return s, dealt
end

local ok, err = pcall(function()
  local h, hpid = human()
  if not h then
    p('d14 deaths: no human player creature')
    return
  end
  if not nGameObject.IsAlive(h) then
    p('d14 deaths: the human is dead (waiting for its respawn); part', tostring((dgsim_d14_deaths_part or 0) + 1),
      'not run at match', f2(nGameSimulator.GetMatchTime()))
    return
  end
  dgsim_d14_deaths_part = (dgsim_d14_deaths_part or 0) + 1
  local part = dgsim_d14_deaths_part
  local team = nGameObject.GetTeam(h)
  local enemy = team == nTeamID.Mortal and nTeamID.Spirit or nTeamID.Mortal
  local enemyPrefix = enemy == nTeamID.Spirit and '^Binding_Spirit' or '^Binding_Mortal'
  local creep = function(id, name) return string.find(name, '^Creep_Jungle_') ~= nil end
  local killer, expected, pre
  if part == 1 then
    killer, expected = nearest(h, creep), 'MinionKilledYou ("A Minion has slain"), recap NPC type 3 or 7, team 0'
  elseif part == 2 then
    killer, expected = nBattlegrounds.GetGuardianOnTeam(enemy), 'TitanKilledYou ("The Guardian has slain"), recap type 13'
  elseif part == 3 then
    killer, expected = nDirector.GetParasiteID(), 'MinionKilledYou ("A Minion has slain"), recap type 8'
  elseif part == 4 then
    killer = nearest(h, function(id, name) return string.find(name, enemyPrefix) ~= nil end)
    expected = 'TowerKilledYou ("A Binding has slain"), recap type 9'
  else
    for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
      local c = nPlayer.GetPlayerControlledObjectID(pid)
      if c ~= nil and c ~= kObjIDNone and nPlayer.IsBot(c) and nGameObject.GetTeam(c) == enemy and
          nGameObject.IsAlive(c) then
        pre = c
        break
      end
    end
    killer = nearest(h, creep)
    expected = 'PlayerKilledYou with the bot as the killer (lastPlayerToHitID), the kill credited to the bot ' ..
      '(death: "killer player N"), its kill gold; the recap with the bot and the creep'
  end
  p(string.format('d14 deaths part %d at match %s: human %s (player %s) hp %s; killer %s; expected %s', part,
    f2(nGameSimulator.GetMatchTime()), tostring(h), tostring(hpid), f2(nGameObject.GetHitPoints(h)), describe(killer),
    expected))
  if not killer or killer == kObjIDNone then
    p('d14 deaths part ' .. part .. ': no killer found')
    return
  end
  if pre then
    local s, dealt = hitFrom(pre, h, 10)
    p(string.format('d14 deaths part %d: first a hit by the bot %s for 10 true: %s, dealt %s', part, describe(pre),
      tostring(s), f2(dealt)))
  elseif part >= 5 then
    p('d14 deaths part ' .. part .. ': no living enemy bot; the creep alone')
  end
  local s, dealt = hitFrom(killer, h, nGameObject.GetHitPoints(h) + 500)
  p(string.format('d14 deaths part %d: lethal hit from %s: %s, dealt %s; human alive %s hp %s', part, describe(killer),
    tostring(s), f2(dealt), tostring(nGameObject.IsAlive(h)), f2(nGameObject.GetHitPoints(h))))
end)
if not ok then p('d14 deaths: ERROR', err) end
return table.concat(out, '\n')
