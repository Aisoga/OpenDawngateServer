-- FIREZONE-live test probe ([host] test_lua=<s>:dgsim_firezone_probe.lua, several times): makes the enemy Guardian cast its area abilities near the bots, so the fire tracker's RecordCircle and
-- RecordLine run and reach the fire zone 0x00877870 (FIREZONE's stub by default). Each run casts the next ability of
-- the list (nAbility.RequestAbility with force, as dgsim_d6c_fire.lua does) on the living bot farthest from its own
-- base: a Mortal bot and the Spirit Guardian, or, with no Mortal bot, a Spirit bot and the Mortal Guardian.
-- LaserBlast and OrbLauncher report a circle (cast type Target), KnockUpOrbs and FireWall a line. The probe logs the
-- cast, the bot's position and nBotHost.IsPositionSafe there; the bots' stats line counts the stub's skipped zones.
local abilities = { 'ability_Guardian_LaserBlast', 'ability_Guardian_KnockUpOrbs', 'ability_Guardian_OrbLauncher',
                    'ability_Guardian_FireWall' }
local out = {}
local function p(s) out[#out + 1] = s end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local ok, err = pcall(function()
  local n = (dgsim_firezone_probe_n or 0) + 1
  dgsim_firezone_probe_n = n
  local name = abilities[(n - 1) % #abilities + 1]
  local function farthest(team, bx, by)
    local best, bestD = nil, -1
    for i = 0, 9 do
      local c = nPlayer.GetPlayerControlledObjectID(i)
      if c ~= nil and c ~= kObjIDNone and nObjectManager.IsValidObject(c) and nPlayer.IsBot(c) and
          nGameObject.IsAlive(c) and nGameObject.GetTeam(c) == team then
        local bp = nGameObject.GetPosition(c)
        local d = math.sqrt((bp.x - bx) ^ 2 + (bp.y - by) ^ 2)
        if d > bestD then best, bestD = c, d end
      end
    end
    return best, bestD
  end
  -- the start points: Mortal about (-235, -230), Spirit the mirror
  local team, casterTeam = nTeamID.Mortal, nTeamID.Spirit
  local bot, fromBase = farthest(nTeamID.Mortal, -235, -230)
  if bot == nil then
    team, casterTeam = nTeamID.Spirit, nTeamID.Mortal
    bot, fromBase = farthest(nTeamID.Spirit, 235, 230)
  end
  local g = nBattlegrounds.GetGuardianOnTeam(casterTeam)
  if g == nil or g == kObjIDNone or bot == nil then
    p('firezone probe ' .. n .. ': no Guardian or no living bot (guardian ' .. tostring(g) .. ', bot ' ..
      tostring(bot) .. ')')
    return
  end
  local bp = nGameObject.GetPosition(bot)
  local r1, r2 = nAbility.RequestAbility(nUtil.SPID(name), g, bot, bp, 1, 0, nAbilitySlot.None, kObjIDNone, true,
                                         false, nil)
  p(string.format('firezone probe %d at match %s s: the team %s Guardian %s casts %s on %s %s (team %s) at (%s, %s), ' ..
    '%s units from its base: RequestAbility -> %s, %s; safe for its team there: %s', n,
    f1(nGameSimulator.GetMatchTime()), tostring(casterTeam), tostring(g), name, tostring(bot),
    tostring(nGameObject.GetAssetName(bot)), tostring(team), f1(bp.x), f1(bp.y), f1(fromBase), tostring(r1),
    tostring(r2),
    tostring(type(nBotHost) == 'table' and nBotHost.IsPositionSafe and nBotHost.IsPositionSafe(team, bp.x, bp.y, bp.z))))
end)
if not ok then p('firezone probe: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
