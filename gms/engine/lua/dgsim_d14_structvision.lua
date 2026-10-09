-- D14b-live probe: a structure's vision (D14b "Structures": "structures' vision"; MATCH_FLOW.md *D14*,
-- *Structures*: the sources are the line-of-sight records with the noun's fowVisibleRadius, Bindings 35.3, and D2g's
-- vision model counts only living sources). [host] test_lua with three calls 2 s apart and a fourth later, e.g.
--   60:dgsim_d14_structvision.lua,62:dgsim_d14_structvision.lua,64:dgsim_d14_structvision.lua,76:dgsim_d14_structvision.lua
-- in a game with bots=0;director_waves=0 and the human at its start point (d14b_fog's), so nothing Mortal but the
-- bottom Binding_Mortal_Outer (28.8, -116.4) sees the spot.
--   part 1: a Spirit melee minion V at (42, -106), 16.8 units from the Binding, immobilized and untargetable (the Binding
--     doesn't shoot it).
--   part 2: expected V visible to Mortal (the Binding's vision; the host's `fog: V ... exits fog for player 0`); then the
--     Binding is killed (KillObject by V).
--   part 3: expected V no longer visible to Mortal (a dead structure gives no vision; `fog: ... enters fog`); V deleted;
--     then the human's shaper beside its own Guardian (-193, -192), for screenshots of the shield effect.
--   part 4 (optional, e.g. 12 s later): the shaper back at its start point.
-- Returns a report; every line starts with "d14 structvision".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd14 structvision ' .. table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f, %.1f)', v.x, v.y) end
local function dist(a, b) return math.sqrt((a.x - b.x) ^ 2 + (a.y - b.y) ^ 2) end

local S = dgsim_d14_structvision or {}
dgsim_d14_structvision = S

local function binding()
  local B = nVector.create(28.8, -116.4, 0)
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local name = nGameObject.GetAssetName(id) or ''
    if string.find(name, '^Binding_Mortal_Outer') and dist(nGameObject.GetPosition(id), B) < 3 then return id end
  end
end

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c end  -- IsBot takes an object
  end
end

local function seen(what, o)
  p(what, tostring(o), o and vec(nGameObject.GetPosition(o)) or '-', 'visible to Mortal',
    tostring(o and nGameObject.IsVisibleToTeam(o, nTeamID.Mortal)), 'to Spirit', tostring(o and nGameObject.IsVisibleToTeam(o, nTeamID.Spirit)))
end

local ok, err = pcall(function()
  S.part = (S.part or 0) + 1
  p('part', S.part, 'at match time', f1(nGameSimulator.GetMatchTime()))
  if S.part == 1 then
    S.b = binding()
    S.v = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Spirit_Melee.Noun'), nTeamID.Spirit, nVector.create(42, -106, 0),
      kZeroVector, 1)
    if S.v and S.v ~= kObjIDNone then
      nAttribute.AddAttributeModifier(S.v, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
      nAttribute.AddAttributeModifier(S.v, nAttributeType.Untargetable, 1, nAttributeOp.kAdd)
    end
    p('Binding', tostring(S.b), S.b and vec(nGameObject.GetPosition(S.b)) or '-', 'alive', tostring(S.b and nGameObject.IsAlive(S.b)),
      '; V', tostring(S.v), 'distance', S.b and S.v and f1(nGameObject.GetObjectDistance(S.b, S.v)) or '-')
  elseif S.part == 2 then
    seen('V with the Binding alive (expected visible to Mortal true):', S.v)
    if S.b then nGameObject.KillObject(S.b, S.v or kObjIDNone, false) end
    p('Binding killed -> alive', tostring(S.b and nGameObject.IsAlive(S.b)))
  elseif S.part == 3 then
    seen('V after the Binding\'s death (expected visible to Mortal false):', S.v)
    if S.v and nGameObject.IsAlive(S.v) then nGameObject.MarkForDelete(S.v) end
    -- the Mortal Guardian's shield (D14's guardian_shield: modifier_Tower_Immunity, whose Tick plays
    -- titan_invulnerability_effect) on screen: the human's shaper 15 units from its own Guardian (-182, -182)
    local h = human()
    if h then
      nLocomotion.TeleportToPositionAndFacing(h, nVector.create(-193, -192, 0), nVector.create(1, 0, 0))
      p('human', tostring(h), 'to', vec(nGameObject.GetPosition(h)), 'beside its own Guardian (screenshots: the shield effect)')
    end
  else
    local h = human()
    if h then
      local pos, orient = nGameSimulator.GetPlayerStartingPoint(nPlayer.GetPlayerIdForObject(h))
      nLocomotion.TeleportToPositionAndOrientation(h, pos, orient)
      p('human home at', vec(nGameObject.GetPosition(h)))
    end
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
