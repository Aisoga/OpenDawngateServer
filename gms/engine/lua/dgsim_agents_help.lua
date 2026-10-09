-- D1b-live test probe (AGENT_AI.md T9, call for help, part 1; [host] test_lua=<s>:dgsim_agents_help.lua): three Spirit
-- melee minions (the victim and two helpers 2.8 units beside it, across the lane) and a Mortal melee minion (the source) 30 units from the
-- victim (up the lane) and about 30 from the helpers: outside their aggroRange (20, edge distance: minus both footprints, 1.41
-- each, d1b_run7) but inside their dropAggroRange
-- (33), so nobody engages it on sight. Placed on the bottom lane between the outer Bindings (mirror of the duel spot).
-- dgsim_agents_help_hit.lua (a second or more later, once the victim's first blackboard update filled its perception
-- list, which Agent_CallForHelp walks) spawns a nearer decoy and hits the victim from the source. Ids in dgsim_help_ids
-- (victim, helper, helper, source; the decoy is appended). Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  local bx, by = dgsim_help_x or 66.5, dgsim_help_y or -68.7
  local spirit = nUtil.GetAsset('Minion_Spirit_Melee.Noun')
  local mortal = nUtil.GetAsset('Minion_Mortal_Melee.Noun')
  local function make(noun, team, x, y)
    return nObjectManager.CreateObject(noun, team, nVector.create(x, y, 0), kZeroVector, 1)
  end
  -- along the bottom lane (direction about (0.73, 0.68) here): spawn points off the navmesh are moved onto it
  -- (d1b_run6/d1b_run8: a source placed across the lane appeared 8.5 units nearer), so only offsets along the lane and
  -- small ones across it keep the distances
  local ux, uy, nx, ny = 0.73, 0.68, -0.68, 0.73
  local victim = make(spirit, nTeamID.Spirit, bx, by)
  local h1 = make(spirit, nTeamID.Spirit, bx + 2.8 * nx, by + 2.8 * ny)
  local h2 = make(spirit, nTeamID.Spirit, bx - 2.8 * nx, by - 2.8 * ny)
  local source = make(mortal, nTeamID.Mortal, bx + 30 * ux, by + 30 * uy)  -- 30 units up the lane
  dgsim_help_ids = {victim, h1, h2, source}
  for i, id in ipairs(dgsim_help_ids) do
    p('help:', i, tostring(id), nGameObject.GetAssetName(id), 'at', vec(nGameObject.GetPosition(id)))
  end
end)
if not ok then p('help: ERROR', err) end
return table.concat(out, '\n')
