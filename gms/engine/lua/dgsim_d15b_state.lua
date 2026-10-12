-- D15b test probe ([host] test_lua=<s>:dgsim_d15b_state.lua, as often as needed): what the
-- items' passives change that the commands file's `report` lines don't show, for player 0's shaper and the enemy bot
-- dgsim_d15_bot_here.lua brought (dgsim_d15_bot):
--   player 0: match time, HP, position, the Q/W/E/R cooldowns left (nAbility.GetCooldownRemaining of each slot's GUID:
--             Zeal's cut), the derived basic attack range (Insight: BasicRangeScale 19), movement speed, basic attacks
--             per second;
--   the bot:  HP, position, the distance walked since the previous run and the speed that makes (a moving victim's
--             slows), the derived movement speed and the attributes 9 MovementSpeedFlatDecrease, 11 ...PercentIncrease,
--             12 ...PercentDecrease, 14 AttackSpeedDecrease, 43 Immobilized (Duress's root);
--   the allies: each allied player's creature with its distance to player 0 and its position (Fluidity's walk).
-- Returns a report (one line per object).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  dgsim_d15b_runs = (dgsim_d15b_runs or 0) + 1
  local now = nGameSimulator.GetMatchTime()
  local me = nPlayer.GetPlayerControlledObjectID(0)
  if me == nil or me == kObjIDNone then
    p('d15b state: no shaper for player 0')
    return
  end
  local mp = nGameObject.GetPosition(me)
  local cds = {}
  for _, s in ipairs({{'Q', nAbilitySlot.Q}, {'W', nAbilitySlot.W}, {'E', nAbilitySlot.E}, {'R', nAbilitySlot.R}}) do
    local g = nAbility.GetAbilityGUIDFromSlot(me, s[2], nAbilityGUIDLookupType.Default)
    local left = (g ~= nil and g ~= kObjIDNone) and nAbility.GetCooldownRemaining(me, g) or nil
    cds[#cds + 1] = s[1] .. ' ' .. f2(left)
  end
  p('d15b state #' .. dgsim_d15b_runs, 'at', f2(now), 'player 0', tostring(me), 'hp', f2(nGameObject.GetHitPoints(me)), 'of',
    f2(nGameObject.GetMaxHitPoints(me)), 'pos', f2(mp.x), f2(mp.y), '; cooldowns left', table.concat(cds, ', '),
    '; basic range', f2(nAttribute.GetDerivedAttributeValue(me, nDerivedAttributeType.BasicAttackRange)),
    'BasicRangeScale', f2(nAttribute.GetAttributeValue(me, nAttributeType.BasicRangeScale)),
    'movespeed', f2(nAttribute.GetDerivedAttributeValue(me, nDerivedAttributeType.Movespeed)),
    'attacks/s', f2(nAttribute.GetDerivedAttributeValue(me, nDerivedAttributeType.BasicAttacksPerSecond)))
  local bot = dgsim_d15_bot
  if bot ~= nil and bot ~= kObjIDNone then
    if nGameObject.IsAlive(bot) then
      local bp = nGameObject.GetPosition(bot)
      local walked, speed = 'n/a', 'n/a'
      local last = dgsim_d15b_botlast
      if last ~= nil and last.id == bot and now > last.t then
        local d = nVector.distance(bp, nVector.create(last.x, last.y, 0))
        walked = f2(d)
        speed = f2(d / (now - last.t))
      end
      dgsim_d15b_botlast = {id = bot, x = bp.x, y = bp.y, t = now}
      local a = function(t) return f2(nAttribute.GetAttributeValue(bot, t)) end
      p('d15b state #' .. dgsim_d15b_runs, 'bot', tostring(bot), nGameObject.GetAssetName(bot), 'hp',
        f2(nGameObject.GetHitPoints(bot)), 'pos', f2(bp.x), f2(bp.y), 'distance', f2(nVector.distance(bp, mp)),
        '; walked', walked, 'since the last run, speed', speed, 'per s; movespeed',
        f2(nAttribute.GetDerivedAttributeValue(bot, nDerivedAttributeType.Movespeed)), 'attributes 9=' ..
        a(nAttributeType.MovementSpeedFlatDecrease), '11=' .. a(nAttributeType.MovementSpeedPercentIncrease), '12=' ..
        a(nAttributeType.MovementSpeedPercentDecrease), '14=' .. a(nAttributeType.AttackSpeedDecrease), '43=' ..
        a(nAttributeType.Immobilized))
    else
      p('d15b state #' .. dgsim_d15b_runs, 'bot', tostring(bot), 'dead')
    end
  end
  local myTeam = nGameObject.GetTeam(me)
  for i = 1, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if id ~= nil and id ~= kObjIDNone and nGameObject.GetTeam(id) == myTeam then
      local ap = nGameObject.GetPosition(id)
      p('d15b state #' .. dgsim_d15b_runs, 'ally', i, tostring(id), nGameObject.GetAssetName(id), 'pos', f2(ap.x), f2(ap.y),
        'distance', f2(nVector.distance(ap, mp)))
    end
  end
end)
if not ok then p('d15b state: ERROR', err) end
return table.concat(out, '\n')
