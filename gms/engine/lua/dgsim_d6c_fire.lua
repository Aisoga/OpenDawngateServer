-- D6c test probe ([host] test_lua=<s>:dgsim_d6c_fire.lua, then again 3-4 s later; AGENT_AI.md section 11.5 B5): a
-- bot dodging a Guardian's area ability. The first run makes the enemy Guardian cast ability_Guardian_LaserBlast
-- (nAbility.RequestAbility with force, the way the tower attack was requested in D1a) on the living bot farthest from
-- its own base: a Mortal bot and the Spirit Guardian, or, with no Mortal bot ([host] bots_allies=0), a Spirit bot and
-- the Mortal Guardian. The ability's own Tick reports its target area to the bot director's fire tracker
-- (nDirector.NotifyFireTrackerOfAbility: cast type Target, the bot's position, radius damageRadius + 11), whose
-- FireTracker_RecordCircle calls FireTracker_RequestAvoidFire: every bot of the other team in the area gets
-- ability_Bot_AvoidFireNP (move to nLocomotion.FindFleeDestination 10-20 units away); 1.5 s later the zone deals its
-- damage over time. The second run reports where the bot stands then, its health, and whether the aimed point is still
-- unsafe for its team (nBotHost.IsPositionSafe). dgsim_d6c_trace.lua (run before) logs the AvoidFireNP Tick and the
-- tracker call.
local out = {}
local function p(s) out[#out + 1] = s end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local ok, err = pcall(function()
  local S = dgsim_d6c_fire
  if S == nil then
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
      p('d6c fire probe: no Guardian or no living bot (guardian ' .. tostring(g) .. ', bot ' .. tostring(bot) .. ')')
      return
    end
    local bp = nGameObject.GetPosition(bot)
    local laser = nUtil.SPID('ability_Guardian_LaserBlast')
    local r1, r2 = nAbility.RequestAbility(laser, g, bot, bp, 1, 0, nAbilitySlot.None, kObjIDNone, true, false, nil)
    dgsim_d6c_fire = { bot = bot, team = team, x = bp.x, y = bp.y, hp = nGameObject.GetHitPoints(bot),
                       t = nGameSimulator.GetMatchTime() }
    p(string.format('d6c fire probe at match %s s: the team %s Guardian %s casts ability_Guardian_LaserBlast on %s %s ' ..
      '(team %s) at (%s, %s), %s units from its base, HP %s: RequestAbility -> %s, %s; safe for its team there: %s',
      f1(nGameSimulator.GetMatchTime()), tostring(casterTeam), tostring(g), tostring(bot),
      tostring(nGameObject.GetAssetName(bot)), tostring(team), f1(bp.x), f1(bp.y), f1(fromBase),
      f1(nGameObject.GetHitPoints(bot)), tostring(r1), tostring(r2),
      tostring(type(nBotHost) == 'table' and nBotHost.IsPositionSafe and nBotHost.IsPositionSafe(team, bp.x, bp.y, bp.z))))
  else
    local b = S.bot
    local bp = nGameObject.GetPosition(b)
    local moved = math.sqrt((bp.x - S.x) ^ 2 + (bp.y - S.y) ^ 2)
    p(string.format('d6c fire probe at match %s s (%s s after the cast): %s now at (%s, %s), %s units from where the ' ..
      'beam was aimed (zone radius 10), HP %s -> %s, alive %s; the aimed point safe for its team: %s, its own spot: %s',
      f1(nGameSimulator.GetMatchTime()), f1(nGameSimulator.GetMatchTime() - S.t), tostring(nGameObject.GetAssetName(b)),
      f1(bp.x), f1(bp.y), f1(moved), f1(S.hp), f1(nGameObject.GetHitPoints(b)), tostring(nGameObject.IsAlive(b)),
      tostring(nBotHost.IsPositionSafe(S.team, S.x, S.y, 0)), tostring(nBotHost.IsPositionSafe(S.team, bp.x, bp.y, bp.z))))
    dgsim_d6c_fire = nil  -- a third run casts again
  end
end)
if not ok then p('d6c fire probe: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
