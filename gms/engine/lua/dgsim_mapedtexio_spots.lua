-- The ground picture's live check ([host] test_lua=<t>:dgsim_mapedtexio_spots.lua before dgsim_mapedtex.lua's calls):
-- sets dgsim_mapedtex_spots to the marks painted into a test map's imported ground picture (2 x 2 tiles of 4096: a
-- ring with a cross at (-172, -172), stripes across the tiles' edges) and the ring's point mirror, where the picture
-- has no mark, so the teleport probe frames each in turn. Report only: one line.
dgsim_mapedtex_spots = {
  {-172.0, -172.0, 'texio: the magenta ring with a cross (tile 0 0)'},
  {172.0, 172.0, 'texio: its point mirror, no mark (the picture is not mirrored)'},
  {0.0, -174.0, 'texio: the stripe across the tiles edge x = 0'},
  {-174.0, 0.0, 'texio: the stripe across the tiles edge y = 0'},
}
return string.format('mapedtexio spots: %d set', #dgsim_mapedtex_spots)
