Maps (@VERSION@)

Put map packages here: a map package is a folder with a map.json in it (for example CM_Ring\map.json). The
release comes without any.

- Players: copy the package into this folder, then start Start-Client again. It builds the map's files into
  your game (about a second per map). The CREATE GAME dialog lists the map when the server offers it.
- Server operators: copy the package here too, then start Start-Server again. Name the map in Start-Server.ini:
  map = <its name> (every match) or custom_maps = <its name> (custom games may pick it). Every player who
  joins a game on it needs the same package: the server hands it to them when they log in (serve_maps).
- Map makers: the map editor makes and tests packages here. Unpack its zip (Dawngate-Map-Editor-<version>.zip)
  into the release folder and double-click Map Editor.cmd ("Making maps" in docs/playing.md of the project's
  repository).

A package that can't be built is left out with a line in the setup's output saying why. Removing a package
removes the map from the lists at the next start.
