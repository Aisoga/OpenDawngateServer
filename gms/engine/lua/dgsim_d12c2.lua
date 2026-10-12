-- D12c-live2: item and perk volumes live. Runs after dgsim_d12.lua (the arena: D.human, D.enemies, D.allies) and after a
-- plan file (written by a scenario generator) that sets the global dgsim_d12c2_steps; each run of this file does the next step.
-- Steps (tables with a kind):
--   request  {name, who='human', rank=1}      nModifier.RequestModifier on `who` with the human as caster
--   ability  {name, at={dx,dy} | target='enemy1'}  nAbility.RequestAbility(name) by the human (a point is an offset from the arena centre)
--   tele     {who, at={dx,dy}}                 teleport to the offset from the arena centre (the volumes' owner moves without a command)
--   hurt     {who='human', fraction=0.1}       one true-damage hit with an enemy's snapshot (OnTookDamage)
--   state    {note, mods={...}, attrs=true}    armor/magic resist/lifedrain of the human, the named modifiers on everyone
--   count    {note, nouns={...}}               live objects whose noun starts with each prefix
-- Every output line starts with "d12c2 <match time>".
dgsim_d12c2 = dgsim_d12c2 or {}
local C = dgsim_d12c2
local D = dgsim_d12

local out
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd12c2 ' .. string.format('%.2f', nGameSimulator.GetMatchTime()) .. ' ' .. table.concat(t, ' ')
end
local function f(v) if type(v) == 'number' then return string.format('%.3f', v) end return tostring(v) end
local function live(id) return id ~= nil and id ~= kObjIDNone and nObjectManager.IsValidObject(id) end
local function pos(id)
  local v = nGameObject.GetPosition(id)
  return string.format('(%.1f, %.1f)', v.x, v.y)
end
local function hexid(id)
  local s = tostring(id)
  if string.sub(s, 1, 2) == '0x' then s = string.sub(s, 3) end
  return string.upper(s)
end

local function who(w)
  if w == 'human' or w == nil then return D.human end
  local k = tonumber(string.match(w, '^enemy(%d)$'))
  if k then return D.enemies[k] end
  k = tonumber(string.match(w, '^ally(%d)$'))
  if k then return D.allies[k] end
  return nil
end

local function centre()
  local at = (D.plan and D.plan.at) or {-100.0, -104.0}
  return at[1], at[2]
end

local function pool()
  local t = {{D.human, 'human'}}
  for i, c in ipairs(D.enemies) do t[#t + 1] = {c, 'enemy' .. i} end
  for i, c in ipairs(D.allies) do t[#t + 1] = {c, 'ally' .. i} end
  return t
end

local function steps_request(s)
  local o = who(s.who)
  local snap = nGameObject.GetAttributeSnapshot(D.human)
  local id = nModifier.RequestModifier(o, D.human, nUtil.SPID(s.name), snap, s.rank or 1, kZeroVector, nil)
  p('request', s.name, 'on', s.who or 'human', hexid(o), '->', tostring(id), '; active',
    tostring(nModifier.AgentHasModifierMatchingGUID(o, nUtil.SPID(s.name))))
end

local function steps_ability(s)
  local at, target = kZeroVector, kObjIDNone
  if s.at then
    local cx, cy = centre()
    at = nVector.create(cx + s.at[1], cy + s.at[2], 0)
  end
  if s.target then target = who(s.target) end
  local r1, inst = nAbility.RequestAbility(nUtil.SPID(s.name), D.human, target, at, 1, 0, nAbilitySlot.None, kObjIDNone,
    true, false, nil)
  p('ability', s.name, 'at', s.at and (s.at[1] .. ',' .. s.at[2]) or '-', '->', tostring(r1), tostring(inst),
    'from', pos(D.human))
end

local function steps_tele(s)
  local o = who(s.who)
  if not live(o) then p('tele', s.who, ': no live object') return end
  local cx, cy = centre()
  local before = nGameObject.GetPosition(o)
  nLocomotion.TeleportToPositionAndFacing(o, nVector.create(cx + s.at[1], cy + s.at[2], before.z), nVector.create(1, 0, 0))
  p('tele', s.who, hexid(o), 'from', string.format('(%.1f, %.1f)', before.x, before.y), 'to', pos(o))
end

local function steps_hurt(s)
  local t = who(s.who)
  local src = (t == D.human) and D.enemies[1] or D.human
  local max = nGameObject.GetMaxHitPoints(t)
  nGameObject.TakeDamage(nGameObject.GetAttributeSnapshot(src), t, max * (s.fraction or 0.1), 0,
    nDescriptors.IsTrueDamage or 0, kZeroVector)
  p('hurt', s.who or 'human', 'hp', f(nGameObject.GetHitPoints(t)), 'of', f(max))
end

local function steps_state(s)
  local h = D.human
  local dist = {}
  for _, e in ipairs(pool()) do
    if e[2] ~= 'human' and live(e[1]) then
      local a, b = nGameObject.GetPosition(h), nGameObject.GetPosition(e[1])
      dist[#dist + 1] = string.format('%s %.1f', e[2], math.sqrt((a.x - b.x) ^ 2 + (a.y - b.y) ^ 2))
    end
  end
  p('state', s.note or '', ': human at', pos(h), 'level', nGameObject.GetPlayerLevel(h), 'distances', table.concat(dist, ', '))
  if s.attrs then
    p('state attrs', s.note or '', ': armor', f(nAttribute.GetAttributeValue(h, nAttributeType.PhysicalDamageReduction)),
      'magic resist', f(nAttribute.GetAttributeValue(h, nAttributeType.MagicalDamageReduction)), 'lifedrain',
      f(nAttribute.GetAttributeValue(h, nAttributeType.LifeDrain)))
  end
  for _, name in ipairs(s.mods or {}) do
    local guid = nUtil.SPID(name)
    local on = {}
    for _, e in ipairs(pool()) do
      if live(e[1]) and nModifier.AgentHasModifierMatchingGUID(e[1], guid) then
        local m = nModifier.GetFirstModifierByGUID(e[1], guid)
        on[#on + 1] = string.format('%s (stacks %s, remaining %s)', e[2], tostring(nModifier.GetStackCount(m)),
          f(nModifier.GetRemainingDuration(m)))
      end
    end
    p('state', name, ':', #on > 0 and table.concat(on, ', ') or 'on nobody')
  end
end

local function steps_count(s)
  local counts, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      for _, prefix in ipairs(s.nouns) do
        if string.sub(name, 1, #prefix) == prefix then
          counts[prefix] = (counts[prefix] or 0) + 1
          p('object', prefix, hexid(id), 'at', pos(id))
        end
      end
    end
  end
  for _, prefix in ipairs(s.nouns) do p('count', s.note or '', prefix, ':', counts[prefix] or 0) end
end

-- fxhook: wraps nEffect.CreateEffect / DestroyEffect so the Foresight aura swaps are logged; the lines are
-- buffered (the calls come from the engine's enter/exit events, between steps) and written at the next step.
local function fxname(fx)
  local names = C.fxnames or {}
  for n, v in pairs(names) do if v == fx then return n end end
  return tostring(fx)
end
local function steps_fxhook(s)
  if C.hooked then p('fxhook already installed') return end
  C.hooked = true
  C.fxnames = {}
  for _, n in ipairs(s.assets or {}) do C.fxnames[n] = nUtil.GetAsset(n) end
  C.buf = {}
  local create, destroy = nEffect.CreateEffect, nEffect.DestroyEffect
  nEffect.CreateEffect = function(a1, fx, owner, ...)
    local r = create(a1, fx, owner, ...)
    pcall(function()
      C.buf[#C.buf + 1] = string.format('d12c2 %.2f fx create %s on %s at %s -> %s', nGameSimulator.GetMatchTime(),
        fxname(fx), hexid(owner), live(owner) and pos(owner) or '(not an object)', tostring(r))
    end)
    return r
  end
  nEffect.DestroyEffect = function(owner, a2, h, ...)
    pcall(function()
      C.buf[#C.buf + 1] = string.format('d12c2 %.2f fx destroy on %s handle %s', nGameSimulator.GetMatchTime(), hexid(owner),
        tostring(h))
    end)
    return destroy(owner, a2, h, ...)
  end
  p('fxhook installed for', table.concat((function() local t = {} for n in pairs(C.fxnames) do t[#t + 1] = n end return t end)(), ', '))
end

function C.step()
  out = {}
  for _, l in ipairs(C.buf or {}) do out[#out + 1] = l end
  C.buf = C.buf and {} or nil
  local ok, err = pcall(function()
    C.k = (C.k or 0) + 1
    local s = (dgsim_d12c2_steps or {})[C.k]
    if s == nil then p('step', C.k, ': none left') return end
    if s.kind == 'request' then steps_request(s)
    elseif s.kind == 'ability' then steps_ability(s)
    elseif s.kind == 'tele' then steps_tele(s)
    elseif s.kind == 'hurt' then steps_hurt(s)
    elseif s.kind == 'state' then steps_state(s)
    elseif s.kind == 'count' then steps_count(s)
    elseif s.kind == 'fxhook' then steps_fxhook(s)
    else p('step', C.k, ': unknown kind', tostring(s.kind)) end
  end)
  if not ok then p('step ERROR', tostring(err)) end
  return table.concat(out, '\n')
end

return dgsim_d12c2.step()
