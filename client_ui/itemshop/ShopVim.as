// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The item shop's own vim symbol (the flame above the sell strip's price), as a class ShopRefund can make.
//
// The symbol is the shop movie's character 250 (the clip it places as mcCoins), which the game doesn't export. The
// setup's added SymbolClass tag binds character 250 to this class, so "new ShopVim()" draws the game's own flame (a
// part of the shop's texture atlas); the class adds nothing to it. The game's mcCoins becomes an instance of it too,
// which changes nothing for the shop (it stays a MovieClip).
package {
    import flash.display.MovieClip;

    public class ShopVim extends MovieClip {
        public function ShopVim() {
            super();
        }
    }
}
