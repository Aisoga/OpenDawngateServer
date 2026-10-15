-- PARAMAP live probe: the parasite's minimap icon (the host's camp icon bits, [host] camp_icons) and its spawn checks.
-- [host] test_lua with this file at several match seconds (global dgsim_paramap counts the calls); the part number
-- picks what it does, so a pilot can run part 1 alone:
--   1 the human teleported to (-18, -10), 20.6 units south-west of the parasite (within its aggro 27 and its ranged
--     gambit's 30 of the map origin), so the camera and the human's vision are on the pit; the parasite reported:
--     position (expected the creature marker (0, 0)), facing (marker rot -92.15), HideHealthBar/Untargetable (unborn);
--   2 (before the 300-s birth, ~290 s) the egg reported again: expected its aggro list EMPTY and the human unhurt;
--   3 (after the birth, ~310 s) born (HideHealthBar 0): expected the list still EMPTY, inCombat false (no aggro on
--     sight, objective_aggro_on_sight 0), the host's "camp icon" line 0x01 -> 0x07 at the birth;
--   4 the human kills it (one true-damage hit from its snapshot): the host's icon line back to 0x01;
--   5 dead, same object, the icon off;
--   6 (after its resurrect at kill + 300 s) alive again, the same object: the icon line 0x07 again.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end
local function vec(v)
  if type(v) ~= 'table' and type(v) ~= 'userdata' then return tostring(v) end
  return string.format('(%.2f, %.2f, %.2f)', v.x or 0, v.y or 0, v.z or 0)
end
local function pos(o)
  local ok, v = pcall(nGameObject.GetPosition, o)
  if not ok or not v then return 'nil' end
  return vec(v)
end
local function attr(o, name)
  local ok, v = pcall(nAttribute.GetAttributeValue, o, nAttributeType[name])
  if ok then return f2(v) end
  return '?'
end
local function call(fn, ...)
  local ok, v = pcall(fn, ...)
  if ok then return v end
  return 'err'
end

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    -- nPlayer.IsBot takes an object (0x00947660), not a player id
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c, pid end
  end
end

local function list(o)
  local ids = {}
  local ok, targets = pcall(nAgent.GetTargetsOnAggroList, o)
  if ok and type(targets) == 'table' then
    for _, t in ipairs(targets) do ids[#ids + 1] = tostring(t) end
  end
  return '[' .. table.concat(ids, ' ') .. ']', #ids
end

local function report(tag, o, h)
  if not o or o == kObjIDNone then
    p('paramap parasite', tag, ': no object')
    return
  end
  local l, n = list(o)
  local facing = call(nGameObject.GetFacing, o)
  local initial = call(nGameObject.GetInitialFacing, o)
  p(string.format('paramap parasite %s: %s %s rank %s alive %s hp %s/%s at %s (initial %s); facing %s initial facing %s; '
    .. 'Untargetable %s HideHealthBar %s; inCombat %s; aggro list %s (%d); human %s at %s dist %s hp %s',
    tag, tostring(o), tostring(nGameObject.GetAssetName(o)), tostring(nGameObject.GetNPCRank(o)),
    tostring(nGameObject.IsAlive(o)), f2(nGameObject.GetHitPoints(o)), f2(nGameObject.GetMaxHitPoints(o)), pos(o),
    vec(call(nGameObject.GetInitialPosition, o)), vec(facing), vec(initial), attr(o, 'Untargetable'),
    attr(o, 'HideHealthBar'), tostring(nGameObject.IsInCombat(o)), l, n, tostring(h), pos(h),
    h and f2(nGameObject.GetObjectDistance(o, h)) or '-', h and f2(nGameObject.GetHitPoints(h)) or '-'))
end

local function hit(h, o, amount)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(h, kObjIDNone)
  local s, dealt = nGameObject.TakeDamage(snap, o, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return s, dealt
end

local ok, err = pcall(function()
  local h, hpid = human()
  local t = nGameSimulator.GetMatchTime()
  local real = nDirector.GetParasiteID()
  dgsim_paramap = (dgsim_paramap or 0) + 1
  local part = dgsim_paramap
  p(string.format('paramap %d at match %s: GetParasiteID %s %s, human %s (player %s)', part, f2(t), tostring(real),
    tostring(real and real ~= kObjIDNone and nGameObject.GetAssetName(real)), tostring(h), tostring(hpid)))
  if part == 1 then
    if h then
      -- facing the pit (north-east)
      nLocomotion.TeleportToPositionAndFacing(h, nVector.create(-18, -10, 0), nVector.create(0.874, 0.486, 0))
    end
    report('p1 unborn, human teleported beside it', real, h)
  elseif part == 2 then
    report('p2 before the birth (expected list EMPTY)', real, h)
  elseif part == 3 then
    report('p3 born (expected list EMPTY, icon on)', real, h)
  elseif part == 4 then
    dgsim_paramap_obj = real
    local s, dealt = hit(h, real, nGameObject.GetHitPoints(real) + 1000)
    p('paramap 4: kill', tostring(s), 'dealt', f2(dealt), 'alive', tostring(nGameObject.IsAlive(real)))
    report('p4 after the kill (icon off)', real, h)
  elseif part == 5 then
    report('p5 dead (icon off)', dgsim_paramap_obj or real, h)
  else
    report('p' .. part .. ' after the resurrect (icon on)', real, h)
    p('paramap', part, 'same object as the killed one:', tostring(tostring(real) == tostring(dgsim_paramap_obj)))
  end
end)
if not ok then p('paramap: ERROR', err) end
return table.concat(out, '\n')
