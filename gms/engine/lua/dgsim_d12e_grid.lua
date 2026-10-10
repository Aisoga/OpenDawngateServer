-- D12e2 probe ([host] test_lua=<s>:dgsim_d12e_grid.lua before each cast, after dgsim_d12.lua and a plan that called
-- dgsim_d12.init()): the hit map's dummy targets. A grid of camp creeps (team NPC, Creep_Jungle_Fish_Minor, rooted with an Immobilized attribute modifier so they stay on their cells) around the arena's centre, from dgsim_d12e_grid_cfg = {x0, x1, y0, y1, step} (offsets from the
-- plan's centre, defaults -9..33 by -12..12 every 3); every call heals the living, puts them back on their cell and
-- replaces the dead. Prints one line: "d12e <t> grid <id>:<x>:<y> ..." with each creep's position after the call, plus
-- the bots ("bot" entries at their own homes), so a log reader can match the host's damage lines to
-- a cell and draw the map of what a cast hit.
local D = dgsim_d12
if D == nil or D.human == nil then return 'd12e grid: no arena' end
local cfg = dgsim_d12e_grid_cfg or {}
local x0, x1, y0, y1, step = cfg.x0 or -8, cfg.x1 or 28, cfg.y0 or -4, cfg.y1 or 4, cfg.step or 2
local at = D.plan.at or {-100.0, -104.0}
local noun = nUtil.GetAsset(D.plan.creepNoun or 'Creep_Jungle_Fish_Minor.Noun')
D.grid = D.grid or {}
local function live(id) return id ~= nil and id ~= kObjIDNone and nObjectManager.IsValidObject(id) end
local parts = {}
local n, made, untarg = 0, 0, 0
local dbg = ''
local ok, err = pcall(function()
  local hx, hy = at[1], at[2]
  for ix = x0, x1, step do
    for iy = y0, y1, step do
      if not (ix == 0 and iy == 0) then
        n = n + 1
        local x, y = hx + ix, hy + iy
        local id = D.grid[n]
        if live(id) and nGameObject.IsAlive(id) then
          local hp, max = nGameObject.GetHitPoints(id), nGameObject.GetMaxHitPoints(id)
          if hp < max then nGameObject.HealDamage(nGameObject.GetAttributeSnapshot(id), id, max - hp + 1, 0, 0, false, false) end
          local pos = nGameObject.GetPosition(id)
          local dx, dy = pos.x - x, pos.y - y
          if dx * dx + dy * dy > 1.0 then
            nLocomotion.TeleportToPositionAndFacing(id, nVector.create(x, y, pos.z), nVector.create(1, 0, 0))
          end
        else
          id = nObjectManager.CreateObject(noun, nTeamID.NPC, nVector.create(x, y, 0), kZeroVector, 1)
          D.grid[n] = id
          made = made + 1
          if live(id) then nAttribute.AddAttributeModifier(id, nAttributeType.Immobilized, 1, nAttributeOp.kAdd) end  -- stand still (no pursuit of the human)
          if live(id) then
            local u = nAttribute.GetAttributeValue(id, nAttributeType.Untargetable)
            if u ~= nil and u > 0 then nAttribute.AddAttributeModifier(id, nAttributeType.Untargetable, -u, nAttributeOp.kAdd) end  -- a created creep starts Untargetable (until its Defend behaviour moves it)
            untarg = untarg + ((u or 0) > 0 and 1 or 0)
            if made <= 3 then dbg = dbg .. string.format(' u%d=%s->%s', made, tostring(u), tostring(nAttribute.GetAttributeValue(id, nAttributeType.Untargetable))) end
          end
        end
        if live(id) then
          local pos = nGameObject.GetPosition(id)
          parts[#parts + 1] = string.format('%s:%.1f:%.1f', (string.gsub(tostring(id), '^0x', '')), pos.x, pos.y)
        end
      end
    end
  end
  for _, c in ipairs(D.enemies or {}) do
    if live(c) then
      local pos = nGameObject.GetPosition(c)
      parts[#parts + 1] = string.format('%s:%.1f:%.1f:bot', (string.gsub(tostring(c), '^0x', '')), pos.x, pos.y)
    end
  end
  for _, c in ipairs(D.allies or {}) do
    if live(c) then
      local pos = nGameObject.GetPosition(c)
      parts[#parts + 1] = string.format('%s:%.1f:%.1f:ally', (string.gsub(tostring(c), '^0x', '')), pos.x, pos.y)
    end
  end
end)
local head = string.format('d12e %.2f grid %d cells, %d made', nGameSimulator.GetMatchTime(), n, made)
local untargetableNote = untarg > 0 and (' untargetable-fixed=' .. untarg .. dbg) or dbg
if not ok then return head .. ' ERROR ' .. tostring(err) end
return head .. ' ' .. table.concat(parts, ' ') .. untargetableNote
