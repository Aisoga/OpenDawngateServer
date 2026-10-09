-- D14b-live probe: do lane minions take neutral jungle creeps as targets? (D14b "Presentation and the minimap";
-- MATCH_FLOW.md *D14*, *Leash and kiting*: lane minions (aggro range 20) count team-0 creeps as hostile,
-- GameObject_ValidateHostileTarget 0x008E22C0, so a creep within 20 of a minion would become its target; open whether
-- the original's minions ignored neutrals). [host] test_lua with this file every 20-30 s of a practice game with bots
-- and waves (d14b_pres's). Each call is a census: every living lane minion's aggro list (entries that are jungle creeps
-- counted and named), and the smallest distance between any living lane minion and any living jungle creep (a creep
-- never within 20 of a lane minion can't be taken on sight). A census with no creep entry and a smallest distance
-- above 20 says the question doesn't arise on SM_Map_v5's lanes; the damage hook can still add a creep that hits a
-- minion (a creep only hits what hit it or its camp).
-- Returns a report; every line starts with "d14 neutral".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd14 neutral ' .. table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f, %.1f)', v.x, v.y) end
local function dist(a, b) return math.sqrt((a.x - b.x) ^ 2 + (a.y - b.y) ^ 2) end

local ok, err = pcall(function()
  local minions, creeps = {}, {}
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      if nGameObject.IsAlive(id) then
        if string.find(name, '^Minion_') or string.find(name, '^Strider_') then
          minions[#minions + 1] = {id = id, name = name, pos = nGameObject.GetPosition(id)}
        elseif string.find(name, '^Creep_Jungle_') then
          creeps[#creeps + 1] = {id = id, name = name, pos = nGameObject.GetPosition(id)}
        end
      end
    end
  end
  local creepSet = {}
  for _, c in ipairs(creeps) do creepSet[tostring(c.id)] = c end
  local onLists, entries = 0, {}
  local best, bm, bc = 1e9, nil, nil
  for _, m in ipairs(minions) do
    local okL, ids = pcall(nAgent.GetTargetsOnAggroList, m.id)
    if okL and type(ids) == 'table' then
      for _, t in ipairs(ids) do
        local c = creepSet[tostring(t)]
        if c then
          onLists = onLists + 1
          if #entries < 8 then
            entries[#entries + 1] = string.format('%s %s at %s -> %s %s at %s (%.1f)', tostring(m.id), m.name, vec(m.pos),
              tostring(c.id), c.name, vec(c.pos), dist(m.pos, c.pos))
          end
        end
      end
    end
    for _, c in ipairs(creeps) do
      local d = dist(m.pos, c.pos)
      if d < best then best, bm, bc = d, m, c end
    end
  end
  p(string.format('census at match %s: %d lane minions, %d jungle creeps alive; creep entries on minions\' aggro lists %d; '
    .. 'nearest minion-creep pair %s', f1(nGameSimulator.GetMatchTime()), #minions, #creeps, onLists,
    bm and string.format('%.1f units: %s %s at %s, %s %s at %s', best, tostring(bm.id), bm.name, vec(bm.pos), tostring(bc.id),
      bc.name, vec(bc.pos)) or '-'))
  for _, e in ipairs(entries) do p('  entry', e) end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
