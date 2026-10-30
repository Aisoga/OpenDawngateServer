# maps

The folder of map packages for a checkout of the code: a map package is a sub-folder with a `map.json` in it (for
example `CM_Ring/map.json`, format 1). No package comes with the code or the release: this folder holds only this
README in git, and everything else in it is ignored (`.gitignore`), so the packages you copy or make here stay yours.

- **Running from a checkout:** the setup scripts and the server take this folder by default. Every package in it is
  built into the game copies (`server/tools/mapsetup --maps maps`) and offered by the server (`dgserver --maps maps`,
  with `--map <name>` or `--custom-maps <names>`).
- **The release** has its own empty `maps` folder beside its programs, with a `README.txt` that tells players and
  operators where packages go (made from `re/scripts/release/maps-README.txt` by the release build).
- **The tests** don't read this folder: the packages they load are fixtures in `server/testdata/maps`
  (`server/internal/testmaps` finds them).

How to write a package and how to install one: `docs/hosting.md` (the maps folder) and `docs/reference.md`.
