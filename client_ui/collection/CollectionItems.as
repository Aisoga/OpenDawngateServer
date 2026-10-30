// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 the OpenDawngateServer contributors.
//
// The Collection's Items: a category "Items" of check boxes in the game's own Collection screen (the kinds Basic,
// Advanced, Legendary, Consumables, then the shop's filter tags) and one "Spells" (All spells, Utility, Offense,
// Defense: the battle spells by their group, on the game's tall cards, as its Spiritstones) that show the match items
// as the Collection's own cards: the item's icon, its name, its cost in vim (the shop's vim flame and the number) and
// its kind and group, in the card's own frame and fonts. The cards are the game's at full size, six to a row on the
// results view's own grid, with only the card's own hover animation. Inside every card of ours the icon keeps an
// in-game size on screen, on a dark square with a thin frame (an item's: the shop's Recommended Items row, ITEM_ICON;
// a spell's: the Spell Book's); the panel's own icon has the size of the shop's Current Selection
// (SHOP_SELECTED_ICON), and its route draws the items as the shop's own tiles (SHOP_TILE_*).
//
// The class sits on the root timeline of the front end's movie (UIRuntime.gfx), placed there by four added tags; the
// game's bytecode stays as it is, and the class uses only public members of the game's classes, by name. Its clip is
// on no stage, so it keeps a static strong reference to itself and a strong ENTER_FRAME listener, and every step is
// guarded. The Collection screen is a view of the component "collectionui" (GComponent.find, the id in the game's
// screen manifest), looked up only while CollectionCard.COLLECTION_SCRIM is set (the screen's constructor sets it,
// its dispose clears it). On the screen it:
//  - asks the screen's menu for one more of its own category buttons (createAssetSet "menuButton", the call the menu
//    makes for the game's categories), fills it as the menu does and keeps it below the game's last category;
//  - on a click of one of its options switches the game's selected options off the way the menu does
//    (nativeBridge.OnMenuStateChanged(category, option, false)), hides the landing page, clears the search terms and
//    gives the screen's results view one CollectionItemData per item (card type 0, id 0, itemType 99);
//  - gives every card that shows one of our items a stub for its two native calls (CollectionCard.Init): the game
//    reads a card's id as an asset pointer, which our items don't have; a card that shows the game's data again gets
//    the game's bridge back;
//  - while our cards show, takes the screen's details listener off (the game's details screens are per collection type
//    and ask C++ for the item) and puts it back once the results are the game's again; a click on one of our cards
//    opens a details panel of our own instead (the item's icon, name, kind, cost, stats, passive and active texts, and
//    the route: the items it builds from and into as the in-match shop's tiles in three columns Basic, Advanced,
//    Legendary, with lines between them; a click on one of those opens that item). The panel is built from the
//    Collection's own art (the details frame and background, the related-search box's background and border, the
//    close button), asked from the screen's asset provider as the menu asks for its buttons, with drawn stand-ins until
//    they come; a click on the dimmed screen around it or the close button closes it;
//  - while our cards show, takes the screen's search field and Sort dropdown first (their change events at a higher
//    priority, stopped there, so C++ neither searches nor sorts its own list over ours): the search greys the items
//    that don't match it (no card leaves the grid), the dropdown holds our sorts (the game's list comes back with its
//    results); in the Items category the kind boxes choose the kinds shown (none checked: all four) and a checked
//    filter tag greys the items without it; the menu's scroll range and wheel count our categories;
//  - places our cards on its own grid every frame and again right after each of the results view's own layouts, so
//    no frame shows the view's layout of the moment, and sizes each new icon right after its load; sizes the
//    results' scroll bar and the mouse wheel's range for its grid and tweens the wheel's scroll as the view does.
// A card (CollectionCard) takes off its hover and click listeners at any Event.REMOVED that bubbles up to it, also one
// of a child deep inside (its _OnRemoved doesn't look at the event's target), until the next ADDED. So every object
// the class puts into or takes out of a card (the vim flame, the icon's dark square) stops its own ADDED and REMOVED
// (quiet), and a card of ours that is shown without its hover listener gets them back (relisten). The route's tiles
// are the class's own Sprites, in no card.
//
// The items come from UI/Scaleform/Collection/dgitems.json in the same added archive (the setup writes it from the
// game's own item data): "dawngate-collitems<TAB>2", the shop's filter tags ("filter<TAB>group<TAB>tag<TAB>title"),
// then "item<TAB>name<TAB>title<TAB>kind<TAB>cost<TAB>group<TAB>icon" lines (kind basic, advanced, legendary,
// consumable or spell; icon the item's ShopIcons texture), each followed by its details ("stat", "passive", "active",
// "from", "into", "tags"; text as colour/text pairs, the colour "n" a line break), "end" (the format:
// re/scripts/patch_login_movie.py's item_list). A line "debug<TAB>1" before the items makes the class report its steps
// as Assert lines in the client's log.
package {
    import flash.display.Bitmap;
    import flash.display.DisplayObject;
    import flash.display.DisplayObjectContainer;
    import flash.display.Graphics;
    import flash.display.Loader;
    import flash.display.MovieClip;
    import flash.display.Shape;
    import flash.display.Sprite;
    import flash.display.Stage;
    import flash.events.Event;
    import flash.events.IOErrorEvent;
    import flash.events.MouseEvent;
    import flash.external.ExternalInterface;
    import flash.filters.GlowFilter;
    import flash.geom.ColorTransform;
    import flash.geom.Point;
    import flash.geom.Rectangle;
    import flash.net.URLLoader;
    import flash.net.URLRequest;
    import flash.text.TextField;
    import flash.text.TextFieldAutoSize;
    import flash.text.TextFormat;
    import flash.text.TextFormatAlign;
    import flash.utils.Dictionary;
    import flash.utils.getDefinitionByName;
    import flash.utils.getQualifiedClassName;
    import flash.utils.getTimer;

    public class CollectionItems extends MovieClip {
        private static const SCREEN_CLASS:String = "com.waystone.components.collectionUI::CollectionView";
        private static const CARD_CLASS:String = "com.waystone.ui.layout.common.controls.CollectionCard";
        private static const COMPONENT_CLASS:String = "com.gravity.framework.component.GComponent";
        private static const COMPONENT_ID:String = "collectionui";
        private static const COMPONENT_NAME:String = "Collection UI Screen";
        private static const DATA_CLASS:String = "com.waystone.model.collection.CollectionItemData";
        private static const MENU_BUTTON_CLASS:String = "maxis.controls::ExpandingOptionsButton";
        private static const DATA_FILE:String = "Collection/dgitems.json";
        private static const MAGIC:String = "dawngate-collitems\t2";
        private static const HEADER:String = "Items";
        private static const CATEGORY_ID:uint = 4242;
        private static const SPELL_HEADER:String = "Spells";
        private static const SPELL_CATEGORY_ID:uint = 4244;
        private static const NO_DETAILS_TYPE:uint = 99;
        private static const SEARCH_EVERY:int = 10;
        private static const OFF_WAIT:int = 30;
        private static const MAX_DEPTH:int = 80;
        // the results view's grid (ResultsView: CARD_UNIT_WIDTH, CARD_UNIT_HGHT, PAD_PER_CARD), ours too (full cards)
        private static const CARD_W:Number = 176, CARD_H:Number = 140, PAD:Number = 10;
        // the spells' cards: the game's Card_1_2 (CollectionCard.CARD_1_2_TYPE_ID, one column and two rows of the grid),
        // the Spiritstones' card, at full size; the results view's own grid corner's top (mMinGridY)
        private static const SPELL_CARD_TYPE:uint = 1;
        private static const GRID_TOP:Number = 25;
        // The item icons at the in-match shop's size: its movie (HUD ItemShopUI.gfx) is a 1920 x 1080 stage scaled to
        // the window and draws a tile's icon 36 x 36 (ItemRenderer) and the Current Selection's 96 x 96
        // (ShowSelectedItem); the front end's stage doesn't scale (Gravity sets NO_SCALE), so its global coordinates
        // are the window's pixels. The textures are 256 x 256 (with mips), so an icon is never drawn larger than its
        // texture. The dark square's margin and frame are in pixels.
        private static const HUD_W:Number = 1920, HUD_H:Number = 1080;
        private static const SHOP_ICON:Number = 36, SHOP_SELECTED_ICON:Number = 96, ICON_TEXTURE:Number = 256;
        // an item's icon on the full-size card: the shop's Recommended Items row (RecommendedItems: 44 x 44), the
        // larger of its two item-icon sizes below the Current Selection (the same share of the card's width as the
        // spells' Spell Book icon on their tall card)
        private static const ITEM_ICON:Number = 44;
        // A card's group line (its info's second line, Card_1_1's info field at 12): a group longer than
        // INFO_FIT_CHARS ("Offense / Utility / Defense") is cut by the card's frame at 12, so it shows at INFO_SMALL
        // (COLLITEMS10; "Offense / Defense" fits at 12)
        private static const INFO_FIT_CHARS:int = 20, INFO_SMALL:int = 9;
        // Card_1_1's two info lines inside the frame in both states (COLLITEMS11). The card's timeline moves its mcInfo
        // from y 44 (IdleOut) to 60 (IdleOver) while the frame's bottom edge moves only 10 units (Collections_Source.gfx,
        // sprite 185), so the hovered card's second line fell 4-5 units below the frame's inner edge and the unhovered
        // card's descenders 2-3. The info's lines are INFO_LEADING apart instead of the field's 2, and the field sits
        // INFO_SHIFT_IDLE units lower on the unhovered card and INFO_SHIFT_OVER on the hovered one, in between with the
        // card's own animation (fitInfo). Spells keep theirs: Card_1_2's info has room.
        private static const INFO_IDLE_Y:Number = 44, INFO_OVER_Y:Number = 60;
        private static const INFO_LEADING:int = -2, INFO_SHIFT_IDLE:Number = 3, INFO_SHIFT_OVER:Number = -1;
        // The shop's item tile (ItemShopRenderer, ITEMSHOP's reading of ItemShopUI.gfx, in its 1920 x 1080 units): a
        // band 128 x 40 at (0, 6), black at 53/255 (no hover change of ours), the icon 36 x 36 at (2, 8) (stretched to
        // its box, kIconSizeStretch: the 256 texture drawn smaller), the name (14, the tier's colour) at (38, 6) and
        // the cost (14, white, glow #FFCC00 3/3) at (38, 26), the selected tile's frame (sh53) at (-2, 4); rows 52
        // apart. The route draws them at the shop's size on screen (shopPixels).
        private static const SHOP_TILE_W:Number = 128, SHOP_TILE_ROW:Number = 52, SHOP_TILE_TEXT:int = 14;
        private static const TILE_BAND_ALPHA:Number = 53 / 255, TILE_FRAME_COLOR:uint = 0x8E8C66;
        private static const TILE_ROWS:int = 8; // the shop's tier lists show 8 rows
        // a spell's icon at the Spell Book's size (HUD SpellBook.gfx: its list icon SB_SpellListIcon places the
        // 54 x 54 SpellIconLoader54 unscaled; the HUD's spell slot frame is 54 x 54 too)
        private static const SPELL_ICON:Number = 54;
        private static const ICON_MARGIN:Number = 3;
        // Prices are in vim, with the shop's own vim flame: sub-image 248 of the shop's atlas ItemShopUI_I12.dds
        // (1024 x 1002, in the HUD's archive), 346,978 to 366,998, the bitmap of shape 249 in the clip the shop places
        // as mcCoins (character 250). A card's price leaves room for it before the number (VIM_GAP); where the atlas
        // can't be loaded, prices read "<n> vim" (the shop's own word: "Sell for 190 vim?").
        private static const VIM_ATLAS:String = "UI/Scaleform/HUD/ItemShopUI_I12.dds";
        private static const VIM_X:Number = 346, VIM_Y:Number = 978, VIM_SIZE:Number = 20;
        private static const VIM_GAP:String = "&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;";
        private static const COST_SIZE:Number = 20; // the panel's cost: its flame's box (the text is 18)
        private static const VIM_RIGHT:Number = 0.8; // the flame's drawn part ends at 16 of its 20 units
        // the wheel's step and the scroll bar's page, as the results view's (mScrollPageSize)
        private static const SCROLL_PAGE:Number = 70;
        // the wheel's scroll as the results view's own (_AnimScroll): the game's TweenLite on the scroll bar's
        // position, SCROLL_TIME_PER_PIXEL (0.001 s) a unit of the distance
        private static const TWEEN_CLASS:String = "com.greensock.TweenLite";
        private static const SCROLL_TIME:Number = 0.001;
        // the menu's (MainMenuView mScrollPageSize)
        private static const MENU_PAGE:Number = 20;
        // search, sort, filters (COLLITEMS4): the search field's submit event (maxis InputField.eTextSubmit), the
        // dropdown's pick (CLIK ListEvent.INDEX_CHANGE), its list class, our sorts, a greyed card's alpha
        private static const SEARCH_SUBMIT:String = "IF_Submit";
        private static const INDEX_CHANGE:String = "listIndexChange";
        private static const DATA_PROVIDER_CLASS:String = "scaleform.clik.data.DataProvider";
        private static const SORTS:Array = ["Default", "Name", "Cost: low to high", "Cost: high to low"];
        private static const GREY_DARK:Number = 0.35; // a greyed card's colour multiplier
        // the details panel: its size (the item's part on the left, the route on the right) and the route's area
        private static const PANEL_W:Number = 1000, PANEL_H:Number = 600, LEFT_W:Number = 520;
        private static const ROUTE_TOP:Number = 86, ROUTE_BOTTOM:Number = 560, ROUTE_MIN_GAP:Number = 44;
        // the Collection's art for the panel (the collection manifest's asset ids): the details view's background and
        // frame, the related-search box's background and border, the details' close button
        private static const ART_IDS:Array = ["detailedViewBackground", "detailedViewFrame", "screenBackground001",
            "basicWidgetBorder003", "detailsCloseButton"];
        // the shop's colours: the tiers' names (FUN_004A7BE0), the passive label, the route's lines (lighter than the
        // shop's #805000 and #336633, which vanish on the dark panel)
        private static const TIER_COLOR:Object = {basic: "#33CC00", advanced: "#0066FF", legendary: "#9932FF"};
        private static const OTHER_COLOR:String = "#E3DDA8";
        private static const SET_PATH:uint = 0xC8A040, UP_PATH:uint = 0x5DB85D;
        private static const TEXT_COLOR:uint = 0xD8D8D8, DIM_COLOR:uint = 0x9A9A9A, GOLD_COLOR:uint = 0xFFCC00;
        private static const FRAME_COLOR:uint = 0x8C7440;
        // the Items category's check boxes: the kinds (none checked: all four), then the item list's filter tags
        private static const KIND_OPTIONS:Array = [
            {label: "Basic", kind: "basic"},
            {label: "Advanced", kind: "advanced"},
            {label: "Legendary", kind: "legendary"},
            {label: "Consumables", kind: "consumable"}];
        private static const ALL_KINDS:Array = ["basic", "advanced", "legendary", "consumable"];
        // the spells' options: label, the item kinds each shows and the group ("" any)
        private static const SPELL_OPTIONS:Array = [
            {label: "All spells", kinds: "spell", group: ""},
            {label: "Utility", kinds: "spell", group: "Utility"},
            {label: "Offense", kinds: "spell", group: "Offense"},
            {label: "Defense", kinds: "spell", group: "Defense"}];

        private static var self:CollectionItems = null; // a strong reference besides the timeline's
        private static var debug:Boolean = false;
        private static var vimFailed:Boolean = false; // the vim flame's atlas didn't load: prices say "vim"
        private static var textSpecs:Dictionary = new Dictionary(true); // our text fields' font, size, colour, align

        private var tick:int = 0;
        private var items:Array = null;      // {name, title, kind, cost, group, icon}
        private var screen:Object = null;    // the CollectionView while it is up
        private var screenBox:DisplayObject = null; // its displayContainer (COLLITEMS8: the class pauses while it is off the stage)
        private var offSince:int = -1;       // the tick the screen's container was first seen off the stage
        private var menuAsked:Object = null; // the screen whose menu was asked for our button
        private var button:Object = null;    // our ExpandingOptionsButton
        private var buttonScreen:Object = null;
        private var ourResults:Array = null; // the array we gave the results view
        private var detailsOff:Boolean = false;
        private var errors:int = 0;
        private var stubbed:Dictionary = new Dictionary(true); // our cards' Init(stub), by card
        // the cards' native calls for our items: the game reads a card's data.id as an asset (OnCardReadyForRender at
        // the end of the hover animation would look the item up by it)
        private var stub:Object = {
            OnCardReadyForRender: function (card:Object, id:uint):int { return -1; },
            OnCardShutdownRender: function (card:Object, id:uint):void {}
        };
        // our grid: its corner (the game's own place of the first card), the cards under the mouse
        private var hovered:Dictionary = new Dictionary(true);
        private var hoverHooked:Dictionary = new Dictionary(true);
        // a card's info field's own y, before fitInfo moved it (given back when the game takes the card again)
        private var infoY:Dictionary = new Dictionary(true);
        private var gridLeft:Number = NaN, gridTop:Number = NaN;
        private var seenLeft:Number = NaN, seenTop:Number = NaN;
        private var gridScreen:Object = null;
        // debug counts, reported every 40 frames: cards we placed again, icons sized, squares added, squares rescaled
        private var moves:int = 0, iconSets:int = 0, iconAdds:int = 0, iconScales:int = 0;
        // the scroll: the results view's scroll bar, our range, whether it must be set again
        private var scrollBar:Object = null;
        private var scrollMax:Number = 0;
        private var scrollDirty:Boolean = true;
        private var wheelBox:Object = null;
        private var wheelTween:Object = null;     // our TweenLite on the scroll bar, its target and its end (getTimer)
        private var wheelTarget:Number = 0;
        private var wheelEnd:int = 0;
        // smooth placement (COLLITEMS6): the late pass after the results view's own drawing (its displayContainer's
        // ENTER_FRAME below the view's own listener), the pass running ("" = the class's frame), what each card was
        // last set to, our icons' Loaders
        private var lateBox:Object = null;
        private var latePhase:String = "";
        private var placedAt:Dictionary = new Dictionary(true); // card holder -> {x, y, s} as read back after our set
        private var styledFor:Dictionary = new Dictionary(true);  // card -> the data its icon was last styled for
        private var iconHooked:Dictionary = new Dictionary(true); // our cards' icon Loaders whose COMPLETE we follow
        private var loaderCard:Dictionary = new Dictionary(true); // their LoaderInfo -> card
        // debug counts: cards placed again by the late pass, its runs, icons sized right after their load
        private var lateMoves:int = 0, lateRuns:int = 0, loadSizes:int = 0;
        // debug (COLLITEMS7): the cards on the stage without their own hover listener, as last reported; cards given
        // their listeners back (relisten)
        private var deafNote:String = "";
        private var relistens:int = 0;

        // the item list's other parts: the items by name, the shop's filter tags ({group, tag, title})
        private var byName:Object = {};
        private var filterTags:Array = [];
        private var dataItem:Dictionary = new Dictionary(true); // our cards' CollectionItemData -> item

        // the details panel (COLLITEMS3): its parts, the Collection's art for it (asset id -> proxy), the route's cards
        private var host:Sprite = null;          // on top of the screen's Sprite: the dimmed screen and the panel
        private var hostScrim:Sprite = null;
        private var hostScreen:Object = null;
        private var panel:Sprite = null;
        private var fallbackLayer:Sprite = null; // drawn stand-ins for the Collection's art
        private var artLayer:Sprite = null;
        private var lineLayer:Sprite = null;
        private var tileLayer:Sprite = null;     // the route's tiles
        private var iconFrame:Sprite = null;
        private var iconLoader:Loader = null;
        private var iconSize:Number = NaN;        // the panel icon's size in the panel's units, as last set
        // the icon at the shop's size (COLLITEMS5): card -> {sprite, px} of the dark square inside its IconLoader
        private var backdrops:Dictionary = new Dictionary(true);
        private var iconNote:String = "";
        private var hoverCard:Object = null;      // the card of ours under the mouse
        private var hoverVim:Sprite = null;       // the vim flame in its price
        private var panelVim:Sprite = null;       // the vim flame before the panel's cost
        private var vimNote:String = "";
        private var vimMeasured:Dictionary = new Dictionary(true); // card -> the price its spaces were measured for
        private var vimSpaces:Dictionary = new Dictionary(true);   // card -> the width of the room before the number
        private var titleTf:TextField = null, kindTf:TextField = null, costTf:TextField = null;
        private var textTf:TextField = null, hintTf:TextField = null, emptyTf:TextField = null;
        private var headerTfs:Array = [];
        private var closeDrawn:Sprite = null;
        private var artProxies:Object = {};
        private var artAdopted:Dictionary = new Dictionary(true);
        private var assetsScreen:Object = null;
        private var routeTiles:Array = [];       // the route's tiles (the shop's, drawn by the class; makeTile)
        private var openItem:Object = null;
        private var soundMan:Object = null;
        private var debugLines:Array = [];
        // search, sort, filters (COLLITEMS4)
        private var searchField:Object = null;   // the screen's search field we listen to
        private var query:String = "";           // our search (lower case, trimmed)
        private var filtersOn:Object = {};       // tag -> checked
        private var kindsOn:Object = {};         // kind -> checked (the Items category's kind boxes)
        private var landing:Boolean = false;     // the last box unchecked: the landing page over our empty set
        private var greyState:int = 0;           // bumped when the search or a filter changes
        private var greyKey:Dictionary = new Dictionary(true); // card -> its item and greyState as last set
        private var sortDrop:Object = null;      // the screen's Sort dropdown we listen to
        private var gameSort:Object = null;      // the game's list, index, rows and scroll bar while ours show
        private var sortIndex:int = 0;
        private var shownKinds:Array = null;     // the kinds of our option shown
        private var shownGroup:String = "";      // and its group ("" any)
        // the spells (COLLSPELLS2): our Spells category; the shown set's grid cell (the spells' tall cards two grid
        // rows, the items' one)
        private var spellButton:Object = null;
        private var cellH:Number = CARD_H;
        private var cornerNote:String = "";
        private var menuWheelBox:Object = null;
        private var menuBar:Object = null;
        private var menuScrollSig:String = "";
        private var menuScrollMax:Number = 0;
        private var debugTf:TextField = null;     // with "debug 1": the last log lines on the dimmed screen

        public function CollectionItems() {
            self = this;
            mouseEnabled = false;
            mouseChildren = false;
            addEventListener(Event.ENTER_FRAME, onFrame, false, 0, false);
            var ld:URLLoader = new URLLoader();
            ld.addEventListener(Event.COMPLETE, function (e:Event):void {
                try {
                    items = parse(String(ld.data));
                    say("items: " + (items == null ? "bad file" : String(items.length)));
                } catch (err:Error) {
                    items = null;
                }
            });
            ld.addEventListener(IOErrorEvent.IO_ERROR, function (e:Event):void {});
            try {
                ld.load(new URLRequest(DATA_FILE));
            } catch (err:Error) {
            }
        }

        // the item list: the items (each with its details) in the file's order, the filter tags into filterTags, the
        // items by name into byName
        private function parse(text:String):Array {
            var rows:Array = text.replace(/\r/g, "").split("\n");
            if (rows.length < 2 || rows[0] != MAGIC) {
                return null;
            }
            var out:Array = [];
            var cur:Object = null;
            filterTags = [];
            byName = {};
            for each (var row:String in rows) {
                var f:Array = row.split("\t");
                var k:String = f[0];
                if (k == "debug" && f.length == 2) {
                    debug = f[1] == "1";
                } else if (k == "filter" && f.length == 4) {
                    filterTags.push({group: f[1], tag: f[2], title: f[3]});
                } else if (k == "item" && f.length == 7) {
                    cur = {name: f[1], title: f[2], kind: f[3], cost: int(f[4]), group: f[5], icon: f[6], stats: [],
                        passives: [], active: null, fromNames: [], intoNames: [], tags: [], order: out.length};
                    out.push(cur);
                    byName[cur.name] = cur;
                } else if (cur == null) {
                    continue;
                } else if (k == "stat") {
                    cur.stats.push(f.slice(1));
                } else if (k == "passive" && f.length >= 3) {
                    cur.passives.push({kind: f[1], title: f[2], runs: f.slice(3)});
                } else if (k == "active" && f.length >= 4) {
                    cur.active = {kind: f[1], title: f[2], cooldown: int(f[3]), runs: f.slice(4)};
                } else if (k == "from" && f.length == 2) {
                    cur.fromNames.push(f[1]);
                } else if (k == "into" && f.length == 2) {
                    cur.intoNames.push(f[1]);
                } else if (k == "tags" && f.length == 2) {
                    cur.tags = String(f[1]).split(" ");
                }
            }
            return out;
        }

        // with "debug 1" in the item list: a line in the client's log (Application.log keeps the first ten of a call
        // site, then thins them)
        private function say(s:String):void {
            if (!debug) {
                return;
            }
            try {
                ExternalInterface.call("Assert", "CI " + tick + " " + s);
            } catch (err:Error) {
            }
            // the log keeps only the first ten lines of a call site: the last ones also on screen, over the panel
            debugLines.push(tick + " " + s);
            if (debugLines.length > 10) {
                debugLines.shift();
            }
            if (debugTf != null) {
                debugTf.text = debugLines.join("\n");
            }
        }

        private function onFrame(e:Event):void {
            tick++;
            try {
                step();
            } catch (err:Error) {
                errors++;
                if (errors <= 20) {
                    say("error " + err.errorID + ": " + err.message);
                }
            }
        }

        private function step():void {
            // COLLITEMS8: the screen's container just left the stage (its dispose, also at the client's quit): no call into
            // the game's code for OFF_WAIT frames, not even the search for the screen
            if (screen != null && (screenBox == null || screenBox.stage == null)) {
                if (offSince < 0) {
                    offSince = tick;
                }
                if (tick - offSince < OFF_WAIT) {
                    return;
                }
            } else {
                offSince = -1;
            }
            if (tick % SEARCH_EVERY == 0) {
                // the screen is a Gravity view (no DisplayObject): its component, by the manifest's id
                var v:Object = scrimSet() ? collectionView() : null;
                if (screen != null && v != screen) {
                    say("Collection screen gone");
                    screen = null;
                    screenBox = null;
                    dropLate();
                    stopWheelTween();
                    hoverCard = null;
                    if (hoverVim != null && hoverVim.parent != null) {
                        hoverVim.parent.removeChild(hoverVim);
                    }
                    detailsOff = false;
                    ourResults = null;
                    scrollBar = null;
                    dropPanel();
                    searchField = null;
                    sortDrop = null;
                    gameSort = null;
                    filtersOn = {};
                    kindsOn = {};
                    landing = false;
                    query = "";
                    menuBar = null;
                    menuWheelBox = null;
                    menuScrollSig = "";
                }
                if (screen == null && v != null) {
                    screen = v;
                    screenBox = v.displayContainer as DisplayObject;
                    say("Collection screen found");
                }
            }
            // COLLITEMS8: nothing while the screen is off the stage (its dispose; at the client's quit FlashUI's last
            // frame comes after the screen's classes are gone, and any call into them crashes)
            if (screen == null || screenBox == null || screenBox.stage == null) {
                return;
            }
            addMenuButton();
            placeButton();
            watchResults();
            hookSearch();
            hookSort();
            guardCards();
            fixScroll();
            fixMenuScroll();
            keepPanelOnTop();
            placeHoverVim();
            hookLatePass();
            countDeaf();
            if (tick % 40 == 0) {
                if (moves + lateMoves + iconSets + iconAdds + iconScales + loadSizes + relistens > 0) {
                    // placed again: cards the class found where it hadn't put them, in its own frame (they may have
                    // been drawn there) and in the late pass right after the view's drawing (moved/passes run)
                    say("40 frames: placed again " + moves + ", late " + lateMoves + "/" + lateRuns + "; icons sized " +
                        iconSets + " (at load " + loadSizes + "), squares added " + iconAdds + ", rescaled " + iconScales +
                        "; listeners given back " + relistens);
                }
                moves = iconSets = iconAdds = iconScales = loadSizes = lateMoves = lateRuns = relistens = 0;
            }
            debugBox();
        }

        // with "debug 1": the last log lines at the bottom left of the screen, over everything of ours
        private function debugBox():void {
            if (!debug) {
                return;
            }
            var container:DisplayObjectContainer = screen.displayContainer as DisplayObjectContainer;
            if (container == null) {
                return;
            }
            if (debugTf == null) {
                debugTf = makeText(12, 0x80FF80);
                debugTf.width = 900;
                debugTf.height = 170;
                debugTf.x = 230;
                debugTf.y = 630;
                debugTf.text = debugLines.join("\n");
            }
            if (debugTf.parent != container || container.getChildIndex(debugTf) != container.numChildren - 1) {
                container.addChild(debugTf);
            }
        }

        // GComponent.find("collectionui") (the screen manifest's id of the Collection's component), its view
        private function collectionView():Object {
            try {
                var gc:Object = getDefinitionByName(COMPONENT_CLASS);
                var comp:Object = gc.find(COMPONENT_ID);
                if (comp == null) {
                    comp = gc.find(COMPONENT_NAME);
                }
                var v:Object = comp == null ? null : comp.view;
                return v != null && getQualifiedClassName(v) == SCREEN_CLASS ? v : null;
            } catch (err:Error) {
            }
            return null;
        }

        private function scrimSet():Boolean {
            try {
                var c:Object = getDefinitionByName(CARD_CLASS);
                return c.COLLECTION_SCRIM != null;
            } catch (err:Error) {
            }
            return false;
        }

        // our category: one more of the menu's own buttons, asked for once per screen when the game's are there
        private function addMenuButton():void {
            if (menuAsked == screen || items == null || items.length == 0) {
                return;
            }
            var menu:Object = screen.mMenuView;
            if (menu == null || menu.viewData == null) {
                return;
            }
            var theirs:Array = menuButtons(menu);
            if (theirs.length == 0 || theirs.length < (menu.viewData as Array).length) {
                return;
            }
            menuAsked = screen;
            button = null;
            var forScreen:Object = screen;
            menu.createAssetSet("menuButton", [], "proxy", 1, function (set:Array):void {
                try {
                    onButtonReady(set, forScreen, theirs[0].parent);
                } catch (err:Error) {
                    say("button failed " + err.errorID + ": " + err.message);
                }
            }, true);
            spellButton = null;
            if (hasKind("spell")) {
                menu.createAssetSet("menuButton", [], "proxy", 1, function (set:Array):void {
                    try {
                        onSpellReady(set, forScreen, theirs[0].parent);
                    } catch (err:Error) {
                        say("spell button failed " + err.errorID + ": " + err.message);
                    }
                }, true);
            }
        }

        private function onButtonReady(set:Array, forScreen:Object, canvas:Object):void {
            if (set == null || set.length == 0 || forScreen != screen) {
                return;
            }
            var proxy:Object = set[0];
            proxy.canvas = canvas;
            var b:Object = proxy.assetInstance;
            if (b == null) {
                return;
            }
            // check boxes (type 1, the menu's MSCCheckBox001, as the game's own Filters): the kinds, then the shop's
            // filter tags in their order
            var opts:Array = [];
            for (var i:int = 0; i < KIND_OPTIONS.length; i++) {
                opts.push({label: KIND_OPTIONS[i].label, id: uint(i)});
            }
            for (var j:int = 0; j < filterTags.length; j++) {
                opts.push({label: filterTags[j].title, id: uint(KIND_OPTIONS.length + j)});
            }
            b.selected = false;
            b.SetOnStateChangeCallback(placeButton);
            b.SetOnOptionClickedCallback(onOption);
            b.SetOptionClass("MSCCheckBox001");
            b.setData({label: HEADER, id: CATEGORY_ID, type: 1, options: opts});
            button = b;
            buttonScreen = forScreen;
            placeButton();
            say("Items button ready");
            askAssets(forScreen);
        }

        // our Spells category (COLLSPELLS2): radio options, below Items
        private function onSpellReady(set:Array, forScreen:Object, canvas:Object):void {
            if (set == null || set.length == 0 || forScreen != screen) {
                return;
            }
            var proxy:Object = set[0];
            proxy.canvas = canvas;
            var b:Object = proxy.assetInstance;
            if (b == null) {
                return;
            }
            var opts:Array = [];
            for (var i:int = 0; i < SPELL_OPTIONS.length; i++) {
                opts.push({label: SPELL_OPTIONS[i].label, id: uint(i)});
            }
            b.selected = false;
            b.SetOnStateChangeCallback(placeButton);
            b.SetOnOptionClickedCallback(onSpellOption);
            b.SetOptionClass("MSCRadioButton003");
            b.setData({label: SPELL_HEADER, id: SPELL_CATEGORY_ID, type: 0, options: opts});
            spellButton = b;
            placeButton();
            say("Spells button ready");
        }

        private function hasKind(kind:String):Boolean {
            for each (var it:Object in items) {
                if (it.kind == kind) {
                    return true;
                }
            }
            return false;
        }

        private function menuButtons(menu:Object):Array {
            var out:Array = [];
            collect(menu.displayContainer as DisplayObject, 0, out); // a BaseView: its Sprite
            for each (var ours:Object in [button, spellButton]) {
                var k:int = ours == null ? -1 : out.indexOf(ours);
                if (k >= 0) {
                    out.splice(k, 1);
                }
            }
            return out;
        }

        // below the game's last category, as the menu's own layout does (3 px apart)
        private function placeButton():void {
            if (button == null || screen == null || buttonScreen != screen) {
                return;
            }
            var theirs:Array = menuButtons(screen.mMenuView);
            var y:Number = 0, x:Number = 0;
            for each (var b:Object in theirs) {
                if (b.visible && b.y + b.height + 3 > y) {
                    y = b.y + b.height + 3;
                    x = b.x;
                }
            }
            if (button.x != x || button.y != y) {
                button.x = x;
                button.y = y;
            }
            // the Spells category below ours
            if (spellButton != null) {
                var sy:Number = button.y + button.height + 3;
                if (spellButton.x != x || spellButton.y != sy) {
                    spellButton.x = x;
                    spellButton.y = sy;
                }
            }
        }

        // a check box of our Items category: a kind box chooses the kinds shown (none checked: all four), a tag box
        // greys the items without every checked tag; any box shows our items (afresh from the game's results, our
        // spells or the landing page), and unchecking the last box shows the landing page, as the game's own check-box
        // category (Filters) does when its last box is unchecked (collitems9_dbg1c)
        private function onOption(catId:uint, optId:uint, isSelected:Boolean):void {
            try {
                var k:int = int(optId) - KIND_OPTIONS.length;
                if (k < 0) {
                    kindsOn[KIND_OPTIONS[optId].kind] = isSelected;
                    say("kind " + KIND_OPTIONS[optId].kind + " " + isSelected);
                } else if (k < filterTags.length) {
                    filtersOn[filterTags[k].tag] = isSelected;
                    greyState++;
                    say("filter " + filterTags[k].tag + " " + isSelected);
                } else {
                    return;
                }
                if (!anyChecked()) {
                    showLanding();
                    return;
                }
                var kinds:Array = checkedKinds();
                var fresh:Boolean = !oursShown() || spellsOnly(shownKinds);
                if (fresh || shownKinds == null || kinds.join(" ") != shownKinds.join(" ")) {
                    showItems(kinds, fresh);
                }
            } catch (err:Error) {
                say("option failed " + err.errorID + ": " + err.message);
            }
        }

        // the kinds of the checked kind boxes in the list's order, or all four when none is checked
        private function checkedKinds():Array {
            var out:Array = [];
            for each (var kind:String in ALL_KINDS) {
                if (kindsOn[kind] === true) {
                    out.push(kind);
                }
            }
            return out.length > 0 ? out : ALL_KINDS.concat();
        }

        private function anyChecked():Boolean {
            for each (var on:Object in [kindsOn, filtersOn]) {
                for (var k:String in on) {
                    if (on[k] === true) {
                        return true;
                    }
                }
            }
            return false;
        }

        // no box checked: the Collection's landing page (the screen's own ShowLandingPage without new text: the
        // Curator's text C++ set last, its welcome unless the player's last search of the game's found nothing) over
        // an empty set of ours, the search emptied; a box checked again, a spell option or the game's own results
        // take it from there
        private function showLanding():void {
            var results:Object = screen.mResultsView;
            landing = true;
            ourResults = [];
            shownKinds = null;
            scrollDirty = true;
            stopWheelTween();
            results.viewData = ourResults;
            if (query != "") {
                query = "";
                greyState++;
            }
            try {
                screen.SetSearchText("");
            } catch (err:Error) {
            }
            screen.sortDropdown.enabled = false;
            var fv:Object = screen.mFeaturedItemsView;
            if (fv != null && !fv.isShown) {
                fv.show();
            }
            say("landing page (no box checked)");
        }

        private function onSpellOption(catId:uint, optId:uint, isSelected:Boolean):void {
            try {
                if (optId < SPELL_OPTIONS.length) {
                    showItems(String(SPELL_OPTIONS[optId].kinds).split(" "), true, SPELL_OPTIONS[optId].group);
                }
            } catch (err:Error) {
                say("spell option failed " + err.errorID + ": " + err.message);
            }
        }

        private static function spellsOnly(kinds:Array):Boolean {
            return kinds != null && kinds.length == 1 && kinds[0] == "spell";
        }

        // our items of the given kinds (and group, "" any) in the picked sort as the results view's data; fresh (the
        // search emptied) when the game's results were shown, else the search, the filters and the sort stay
        private function showItems(kinds:Array, fresh:Boolean = true, group:String = ""):void {
            var dataClass:Class = getDefinitionByName(DATA_CLASS) as Class;
            var out:Array = [];
            var list:Array = [];
            for (var i:int = 0; i < items.length; i++) {
                if (kinds.indexOf(items[i].kind) >= 0 && (group == "" || items[i].group == group)) {
                    list.push(items[i]);
                }
            }
            for each (var it:Object in sorted(list)) {
                var d:Object = cardData(dataClass, it);
                dataItem[d] = it;
                out.push(d);
            }
            var fromGame:Boolean = ourResults == null;
            shownKinds = kinds;
            shownGroup = group;
            // the spells on the game's tall cards (two grid rows), the items on its Card_1_1 (one); a set is one or the
            // other, both at full size
            var spells:Boolean = spellsOnly(kinds);
            cellH = spells ? 2 * CARD_H + PAD : CARD_H;
            if (fresh) {
                // our other category's options off (the game's own are released below)
                var other:Object = spells ? button : spellButton;
                if (other != null) {
                    other.ClearOptionStates();
                }
                if (spells) {
                    kindsOn = {};
                    filtersOn = {};
                    greyState++;
                }
                releaseGameOptions();
                screen.HideLandingPage();
                screen.ClearOptionStatesSelectOne();
                screen.SetSearchTerms([]);
                if (fromGame) {
                    query = "";
                    greyState++;
                    try {
                        screen.SetSearchText("");
                    } catch (err:Error) {
                    }
                }
                ourSorts();
            }
            var results:Object = screen.mResultsView;
            if (!detailsOff) {
                results.signalRequestDetails.remove(screen.OpenDetailsByItemData);
                results.signalRequestDetails.add(onCard);
                detailsOff = true;
            }
            ourResults = out;
            landing = false;
            scrollDirty = true;
            stopWheelTween();
            guardCards();
            results.viewData = out;
            try {
                results.SetScrollbarPosition(0);
            } catch (err:Error) {
            }
            say("results: " + out.length + " cards (" + kinds.join(",") + (group != "" ? " " + group : "") + ")");
        }

        // the game's own selected options (its filter state in C++) switched off the way the menu does it
        // (MainMenuView: nativeBridge.OnMenuStateChanged(category, option, false)), so the game and the menu agree and
        // its next option starts from nothing
        private function releaseGameOptions():void {
            var bridge:Object = screen.nativeBridge;
            for each (var b:Object in menuButtons(screen.mMenuView)) {
                var cat:Object = b.getData();
                if (cat == null) {
                    continue;
                }
                var opts:Array = [];
                collectOptions(b as DisplayObject, b.mcToggleButton, 0, opts);
                for each (var o:Object in opts) {
                    if (o.selected) {
                        bridge.OnMenuStateChanged(uint(cat.id), uint(o.data.id), false);
                    }
                }
            }
        }

        private function collectOptions(o:DisplayObject, toggle:Object, depth:int, out:Array):void {
            if (o == null || depth > 12) {
                return;
            }
            if (o != toggle && depth > 0 && "selected" in o && "data" in o) {
                var d:Object = Object(o).data;
                if (d != null && "id" in d && "label" in d) {
                    out.push(o);
                    return;
                }
            }
            var c:DisplayObjectContainer = o as DisplayObjectContainer;
            if (c == null) {
                return;
            }
            for (var i:int = 0; i < c.numChildren; i++) {
                try {
                    collectOptions(c.getChildAt(i), toggle, depth + 1, out);
                } catch (err:Error) {
                }
            }
        }

        private static function kindLabel(kind:String):String {
            switch (kind) {
                case "basic": return "Basic Item";
                case "advanced": return "Advanced Item";
                case "legendary": return "Legendary Item";
                case "consumable": return "Consumable";
                case "spell": return "Spell";
            }
            return kind;
        }

        // a card's data: the game's CollectionItemData for one of our items
        private static function cardData(dataClass:Class, it:Object):Object {
            return new dataClass({
                id: 0, // the game reads id as an asset pointer (OnCardReadyForRender): 0 is its null case
                type: it.kind == "spell" ? SPELL_CARD_TYPE : 0, // the card's size: Card_1_2 for a spell, else Card_1_1
                itemType: NO_DETAILS_TYPE,
                title: it.title,
                longName: it.title,
                // the price in vim: room for the shop's flame before the number (placeHoverVim), or the shop's word
                price: it.cost > 0 ? (vimFailed ? it.cost + " vim" : VIM_GAP + it.cost) : "",
                info: infoHtml(it),
                image: it.icon,
                isLocked: false,
                isFreeRot: false,
                isNew: false,
                isSale: false,
                isGenerated: false,
                canBuy: false
            });
        }

        // a card's info: its kind, then its group (the line the unhovered card shows below its title bar); an item's
        // lines INFO_LEADING apart (fitInfo places them)
        private static function infoHtml(it:Object):String {
            var s:String = kindLabel(it.kind) + (it.group == "" ? "" : "<br>" + (it.group.length > INFO_FIT_CHARS ?
                "<font size=\"" + INFO_SMALL + "\">" + it.group + "</font>" : it.group));
            return it.kind == "spell" ? s : "<textformat leading=\"" + INFO_LEADING + "\">" + s + "</textformat>";
        }

        // a click on one of our cards: our own details panel (the game's details screens ask C++ for the item)
        private function onCard(data:Object):void {
            try {
                var it:Object = dataItem[data];
                say("card clicked: " + (it == null ? "?" : it.name));
                if (it != null) {
                    openPanel(it);
                }
            } catch (err:Error) {
                say("panel failed " + err.errorID + ": " + err.message);
            }
        }

        // every card that shows one of our items talks to the stub, every other card to the game again (cards are
        // pooled: a card can change from our item to the game's and back); CollectionCard.Init(ext) is its only setter
        private function guardCards():void {
            var results:Object = screen.mResultsView;
            // the cards' proxies are parented to the view's public Box resultsRootBox (ResultsView's placement)
            var root:DisplayObject = results == null ? null : results.resultsRootBox as DisplayObject;
            if (root == null) {
                return;
            }
            var cards:Array = [];
            collectCards(root, 0, cards);
            // in a late pass (right after the view's own drawing) only what the view changed: a card it placed or
            // gave new data
            var late:Boolean = latePhase != "";
            for each (var c:Object in cards) {
                var d:Object = c.data;
                var ours:Boolean = d != null && uint(d.itemType) == NO_DETAILS_TYPE;
                var changed:Boolean = !late;
                if (ours && stubbed[c] !== true) {
                    c.Init(stub);
                    stubbed[c] = true;
                    changed = true;
                } else if (!ours && stubbed[c] === true) {
                    c.Init(results.nativeBridge);
                    delete stubbed[c];
                    unstyle(c, root as DisplayObjectContainer);
                }
                if (ours) {
                    relisten(c);
                    if (place(c, root as DisplayObjectContainer)) {
                        changed = true;
                    }
                    if (changed || styledFor[c] !== d) {
                        styledFor[c] = d;
                        styleIcon(c);
                    }
                    greyCard(c, dataItem[d]);
                    fitInfo(c, dataItem[d]);
                }
            }
        }

        // --- smooth placement (COLLITEMS6) ---

        // The results view lays its cards out on its own grid in its validation (BaseView.invalidate adds an
        // ENTER_FRAME listener on the view's displayContainer; validate -> draw -> ResultsView.onDraw ->
        // _UpdateCardData, _UpdateCardPositions), which runs after the class's own ENTER_FRAME, so that frame would
        // show the game's layout and the class would put the cards back only the next one. So the class places its
        // cards again right after the view's drawing: in an ENTER_FRAME listener on the same container below the
        // view's own (one dispatcher runs its listeners by priority). Seen with the debug counts: the late pass
        // re-placed every card the view laid out, and the class's own frame then found none out of place.
        // COLLITEMS8: the listener lives only while the container is on the stage. ENTER_FRAME reaches a display object
        // on no stage too, and the container outlives the Collection screen: at the client's quit the front end's
        // last frame (FlashUI's shutdown) called onLateFrame on it after the class's code was gone, and crashed
        // (0x00EE3BB8). So it goes with the container's REMOVED_FROM_STAGE (the screen's dispose) and the screen.
        private function hookLatePass():void {
            var results:Object = screen.mResultsView;
            var dc:DisplayObject = results == null ? null : results.displayContainer as DisplayObject;
            if (dc != null && dc != lateBox && dc.stage != null) {
                dropLate();
                lateBox = dc;
                dc.addEventListener(Event.ENTER_FRAME, onLateFrame, false, -1000, false);
                dc.addEventListener(Event.REMOVED_FROM_STAGE, onLateBoxGone, false, 0, false);
                say("late pass on the results view's container");
            }
        }

        // every listener of the class on the game's objects that a frame or a teardown can call: the late pass and
        // the icon Loaders' COMPLETE (hooked again when the screen's cards show again)
        private function dropLate():void {
            if (lateBox != null) {
                lateBox.removeEventListener(Event.ENTER_FRAME, onLateFrame);
                lateBox.removeEventListener(Event.REMOVED_FROM_STAGE, onLateBoxGone);
                lateBox = null;
            }
            for (var ld:* in iconHooked) {
                var loader:Loader = ld as Loader;
                if (loader != null) {
                    loader.contentLoaderInfo.removeEventListener(Event.COMPLETE, onCardIconLoaded);
                }
            }
            iconHooked = new Dictionary(true);
            loaderCard = new Dictionary(true);
            styledFor = new Dictionary(true); // styleIcon hooks a card's Loader again
        }

        private function onLateBoxGone(e:Event):void {
            try {
                dropLate();
                say("late pass dropped (the results view left the stage)");
            } catch (err:Error) {
            }
        }

        // our cards are (or were until now) in the results view
        private function wantsLate():Boolean {
            if (screen == null || screen.mResultsView == null) {
                return false;
            }
            if (ourResults != null) {
                return true;
            }
            for (var k:* in stubbed) {
                return true;
            }
            return false;
        }

        private function onLateFrame(e:Event):void {
            if (lateBox == null || lateBox.stage == null) {
                dropLate();
                return;
            }
            if (!wantsLate()) {
                return;
            }
            lateRuns++;
            latePhase = "late";
            try {
                guardCards();
                fixScroll();
            } catch (err:Error) {
                errors++;
                if (errors <= 20) {
                    say("error in the late pass " + err.errorID + ": " + err.message);
                }
            }
            latePhase = "";
        }

        // the card's own placed object: the card, or a wrapper that holds only it (below resultsRootBox the cards share
        // one content Sprite)
        private static function holderOf(c:Object, root:DisplayObjectContainer):DisplayObject {
            var o:DisplayObject = c as DisplayObject;
            while (o != null && o.parent != null && o.parent != root && o.parent.numChildren == 1) {
                o = o.parent;
            }
            return o != null && o.parent != null && o.parent != root ? o : null;
        }

        // the game's grid width (ResultsView: as many full cards as fit, PAD apart)
        private function gridWidth():Number {
            var w:Number = NaN;
            try {
                w = Number(screen.mResultsView.bounds.width);
            } catch (err:Error) {
            }
            var cols:int = isNaN(w) ? 6 : Math.max(1, Math.floor((w - PAD) / (CARD_W + PAD)));
            return cols * CARD_W + (cols - 1) * PAD;
        }

        // our columns across the grid: the view's own (six at the usual width)
        private function columns(gridW:Number):int {
            return Math.max(1, Math.floor((gridW + PAD) / (CARD_W + PAD)));
        }

        // our cards at full size on our grid (the view's own columns), rows a cell (cellH: one grid row for the items'
        // cards, two for the spells') apart, in our order (the view would leave a tall card's second row to the next
        // card); the one under the mouse above the others, so the card's own hover animation isn't covered; true when
        // the card wasn't where the class had put it (placed by the results view, or new)
        private function place(c:Object, root:DisplayObjectContainer):Boolean {
            var t:DisplayObject = holderOf(c, root);
            if (t == null || ourResults == null) {
                return false;
            }
            if (gridScreen != screen) {
                gridScreen = screen;
                gridLeft = NaN;
            }
            var idx:int = ourResults.indexOf(c.data);
            if (idx < 0) {
                return false;
            }
            var offX:Number = Number(c.TopLeftOffsetX), offY:Number = Number(c.TopLeftOffsetY);
            if (isNaN(gridLeft)) {
                // the game's own place of the first card gives the grid's corner, once the results view has placed
                // it (a new card sits at 0,0 first; the grid's top is about 25): right after the view's drawing (the
                // late pass on its container), else the same place two frames running
                if (idx != 0 || t.scaleX != 1) {
                    return false;
                }
                var px:Number = t.x - offX, py:Number = t.y - offY;
                if (py < 20 || (latePhase != "late" && (px != seenLeft || py != seenTop))) {
                    seenLeft = px;
                    seenTop = py;
                    return false;
                }
                // the top is the view's constant (mMinGridY): after the Spiritstones its first card of ours came a
                // row lower (COLLSPELLS2, collspells2_ref1c), and the class's grid with it
                gridLeft = px;
                gridTop = GRID_TOP;
                scrollDirty = true;
                var cn:String = "grid corner " + px + "," + py + " (top " + GRID_TOP + ")";
                if (cn != cornerNote) {
                    cornerNote = cn;
                    say(cn);
                }
            }
            if (hoverHooked[c] !== true) {
                c.mouseOver.add(onCardOver);
                c.mouseOut.add(onCardOut);
                hoverHooked[c] = true;
            }
            var gridW:Number = gridWidth();
            var cols:int = columns(gridW);
            var col:int = idx % cols, row:int = int(idx / cols);
            var stepX:Number = cols > 1 ? (gridW - CARD_W) / (cols - 1) : 0;
            var nx:Number = Math.round(gridLeft + col * stepX + offX), ny:Number = Math.round(gridTop + row * (cellH + PAD) + offY);
            if (hovered[c] === true && t.parent.getChildIndex(t) != t.parent.numChildren - 1) {
                t.parent.setChildIndex(t, t.parent.numChildren - 1);
            }
            // what the class set last, as read back (the player rounds a position to twips)
            var p:Object = placedAt[t];
            var moved:Boolean = p == null || t.x != p.x || t.y != p.y || t.scaleX != p.s;
            if (moved || Math.abs(t.x - nx) > 0.04 || Math.abs(t.y - ny) > 0.04 || t.scaleX != 1 || t.scaleY != 1) {
                if (moved) {
                    // placed by the results view (which sets its scroll bar then) or new
                    scrollDirty = true;
                    if (latePhase == "") {
                        moves++;
                    } else {
                        lateMoves++;
                    }
                }
                t.scaleX = t.scaleY = 1;
                t.x = nx;
                t.y = ny;
                placedAt[t] = {x: t.x, y: t.y, s: t.scaleX};
            }
            return moved;
        }

        private function onCardOver(c:Object):void {
            hovered[c] = true;
            hoverCard = c;
            if (debug) {
                var it:Object = dataItem[c.data];
                say("over " + (it == null ? "?" : it.name));
            }
        }

        // --- cards that keep their listeners (COLLITEMS7) ---

        // CollectionCard._OnAdded puts on its ROLL_OVER, ROLL_OUT and MOUSE_DOWN listeners (and one for REMOVED);
        // _OnRemoved takes them off (and listens for ADDED again) at any REMOVED that reaches the card, also one that
        // bubbles up from a child deep inside, without looking at the target. Our flame leaving a card's price took the
        // card's hover and click away until its icon's next load (an ADDED inside it), and our square leaving a pooled
        // card took them from the game's own card. So every object of ours that goes into a card stops its own ADDED
        // and REMOVED (and those of its children: the flame's atlas Loader) at itself.
        private static function quiet(o:DisplayObject):void {
            o.addEventListener(Event.ADDED, stopHere, false, 0, false);
            o.addEventListener(Event.REMOVED, stopHere, false, 0, false);
        }

        private static function stopHere(e:Event):void {
            e.stopPropagation();
        }

        // a card of ours on the stage without its hover listener (the state _OnRemoved leaves, for a REMOVED the
        // class didn't stop, e.g. of the game's own): a non-bubbling ADDED on the card itself runs its _OnAdded
        private function relisten(c:Object):void {
            try {
                if (c.stage != null && !c.hasEventListener(MouseEvent.ROLL_OVER) && c.hasEventListener(Event.ADDED)) {
                    c.dispatchEvent(new Event(Event.ADDED, false, false));
                    relistens++;
                }
            } catch (err:Error) {
            }
        }

        // with "debug 1": how many cards on the stage lack their own ROLL_OVER listener (CollectionCard._OnRemoved
        // took it off: such a card neither hovers nor takes a click), ours and the game's, when it changes
        private function countDeaf():void {
            if (!debug) {
                return;
            }
            var results:Object = screen.mResultsView;
            var root:DisplayObject = results == null ? null : results.resultsRootBox as DisplayObject;
            var cards:Array = [];
            if (root != null) {
                collectCards(root, 0, cards);
            }
            var o:int = 0, on:int = 0, g:int = 0, gn:int = 0;
            for each (var c:Object in cards) {
                if (c.stage == null) {
                    continue;
                }
                var deaf:Boolean = !c.hasEventListener(MouseEvent.ROLL_OVER);
                if (stubbed[c] === true) {
                    on++;
                    o += deaf ? 1 : 0;
                } else {
                    gn++;
                    g += deaf ? 1 : 0;
                }
            }
            var note:String = "deaf cards: ours " + o + "/" + on + ", game " + g + "/" + gn;
            if (note != deafNote) {
                deafNote = note;
                say(note);
            }
        }

        private function onCardOut(c:Object):void {
            delete hovered[c];
            if (hoverCard == c) {
                hoverCard = null;
            }
        }

        private function unstyle(c:Object, root:DisplayObjectContainer):void {
            if (greyKey[c] !== undefined) {
                delete greyKey[c];
                c.alpha = 1;
                c.transform.colorTransform = new ColorTransform();
                try {
                    c.mcImage.ClearFilters();
                } catch (err:Error) {
                }
            }
            if (hoverHooked[c] === true) {
                c.mouseOver.remove(onCardOver);
                c.mouseOut.remove(onCardOut);
                delete hoverHooked[c];
                delete hovered[c];
            }
            delete styledFor[c];
            if (infoY[c] !== undefined) {
                try {
                    c.mcInfo.textField.y = Number(infoY[c]);
                } catch (err:Error) {
                }
                delete infoY[c];
            }
            var t:DisplayObject = holderOf(c, root);
            if (t != null) {
                t.scaleX = t.scaleY = 1;
            }
            unstyleIcon(c);
        }

        // --- the icon at the in-match shop's size (COLLITEMS5) ---

        // a size of the shop's movie (its units) in the window's pixels: the HUD movie's 1920 x 1080 stage fits the
        // window (24 pixels for a tile's icon at 720 lines, 27 at 810, 36 at 1080), never above the texture's size
        private static function shopPixels(units:Number, o:DisplayObject):Number {
            var st:Stage = o == null ? null : o.stage;
            if (st == null || st.stageWidth < 1 || st.stageHeight < 1) {
                return NaN;
            }
            var px:Number = Math.round(units * Math.min(st.stageWidth / HUD_W, st.stageHeight / HUD_H));
            return Math.max(8, Math.min(ICON_TEXTURE, px));
        }

        // the window's pixels per unit of an object's own coordinates (the front end's stage doesn't scale): the product of
        // the scales up to the stage, exact (localToGlobal rounds to twips, so a size taken from it could change by a
        // rounding from frame to frame)
        private static function pixelScale(o:DisplayObject):Number {
            var k:Number = 1;
            for (var p:DisplayObject = o; p != null && !(p is Stage); p = p.parent) {
                k *= p.scaleX;
            }
            return k;
        }

        // a card of ours: its icon at an in-game size in pixels, whatever the card's scale (also during the card's own
        // hover animation), centred where the card's own icon sits (the IconLoader's origin: with kIconSizeCenterSize
        // it centres its Loader on 0,0), on a dark square with a thin frame below the Loader inside the IconLoader (as
        // the card's sibling it drew above the icon: round 2's variant A)
        private function styleIcon(c:Object):void {
            var img:DisplayObjectContainer = c.mcImage as DisplayObjectContainer;
            if (img == null || img.stage == null) {
                return;
            }
            // a spell's icon at the Spell Book's size, an item's at the shop's Recommended Items row's
            var it:Object = c.data == null ? null : dataItem[c.data];
            var px:Number = shopPixels(it != null && it.kind == "spell" ? SPELL_ICON : ITEM_ICON, img);
            var k:Number = pixelScale(img);
            if (isNaN(px) || !(k > 0.01)) {
                return;
            }
            var u:Number = px / k;
            var ld:Object = c.mcImage.loader;
            var loader:Loader = ld as Loader;
            if (loader != null && iconHooked[loader] !== true) {
                // a new icon (the card's next data) is sized by the IconLoader's own COMPLETE listener to 70 % of the
                // card (CollectionCard.setData), which would show until the class's next frame: size it right after
                iconHooked[loader] = true;
                loaderCard[loader.contentLoaderInfo] = c;
                loader.contentLoaderInfo.addEventListener(Event.COMPLETE, onCardIconLoaded, false, -1000, false);
            }
            if (ld != null && ld.content != null && (Math.abs(ld.width - u) > 0.25 || Math.abs(ld.height - u) > 0.25)) {
                ld.width = u;
                ld.height = u;
                ld.x = -u / 2;
                ld.y = -u / 2;
                iconSets++;
            }
            var bd:Object = backdrops[c];
            if (bd == null) {
                var sp:Sprite = new Sprite();
                sp.mouseEnabled = false;
                quiet(sp); // its ADDED and REMOVED would reach the card (COLLITEMS7)
                bd = {sprite: sp, px: 0};
                backdrops[c] = bd;
            }
            var s:Sprite = bd.sprite;
            if (s.parent != img) {
                img.addChildAt(s, 0);
                iconAdds++;
            } else if (img.getChildIndex(s) != 0) {
                img.setChildIndex(s, 0);
                iconAdds++;
            }
            if (bd.px != px) {
                // drawn in pixels: the square's own scale undoes the card's
                bd.px = px;
                var h:Number = px / 2 + ICON_MARGIN;
                var g:Graphics = s.graphics;
                g.clear();
                g.lineStyle(1, FRAME_COLOR, 1, true);
                g.beginFill(0x0E0C0E, 0.92);
                g.drawRect(-h, -h, 2 * h, 2 * h);
                g.endFill();
            }
            if (Math.abs(s.scaleX * k - 1) > 0.005) {
                s.scaleX = s.scaleY = 1 / k;
                iconScales++;
            }
            var note:String = "icon " + px + " px, stage " + img.stage.stageWidth + "x" + img.stage.stageHeight;
            if (note != iconNote) {
                iconNote = note;
                say(note + " (card x" + Math.round(k * 1000) / 1000 + ")");
            }
        }

        // a card's icon loaded (after the IconLoader's own listener, which sized it the game's way): the shop's size
        // again at once, if the card is still ours (styled)
        private function onCardIconLoaded(e:Event):void {
            try {
                var c:Object = loaderCard[e.target];
                if (c != null && backdrops[c] != null) {
                    var before:Number = Number(c.mcImage.loader.width);
                    styleIcon(c);
                    if (Number(c.mcImage.loader.width) != before) {
                        loadSizes++;
                    }
                }
            } catch (err:Error) {
            }
        }

        // a card that shows the game's data again: no square (its next icon gets the game's size from its IconLoader)
        private function unstyleIcon(c:Object):void {
            var bd:Object = backdrops[c];
            if (bd == null) {
                return;
            }
            delete backdrops[c];
            var s:Sprite = bd.sprite;
            if (s.parent != null) {
                s.parent.removeChild(s);
            }
        }

        // --- prices in vim with the shop's flame (COLLITEMS5) ---

        // The shop's vim symbol at VIM_SIZE x VIM_SIZE units (scale it to the size wanted): its atlas through a Loader,
        // masked to the flame's sub-image. Few exist (the hovered card's price, the panel's cost, one per route tile, the
        // tiles pooled), so each loads the atlas itself; a failed load makes every later price read "<n> vim".
        private function makeVim():Sprite {
            var sp:Sprite = new Sprite();
            sp.mouseEnabled = sp.mouseChildren = false;
            quiet(sp); // in a card's price its ADDED and REMOVED (and its atlas's) would reach the card (COLLITEMS7)
            var ld:Loader = new Loader();
            ld.x = -VIM_X;
            ld.y = -VIM_Y;
            var m:Shape = new Shape();
            m.graphics.beginFill(0);
            m.graphics.drawRect(0, 0, VIM_SIZE, VIM_SIZE);
            m.graphics.endFill();
            sp.addChild(ld);
            sp.addChild(m);
            ld.mask = m;
            ld.contentLoaderInfo.addEventListener(Event.COMPLETE, function (e:Event):void {
                var bmp:Bitmap = ld.content as Bitmap;
                if (bmp != null) {
                    bmp.smoothing = true;
                }
                say("vim flame: atlas " + Math.round(ld.width) + "x" + Math.round(ld.height));
            }, false, 0, false);
            ld.contentLoaderInfo.addEventListener(IOErrorEvent.IO_ERROR, function (e:Event):void {
                vimFailed = true;
                sp.visible = false;
                say("vim flame: the atlas didn't load");
            }, false, 0, false);
            try {
                ld.load(new URLRequest("img://" + VIM_ATLAS));
            } catch (err:Error) {
                vimFailed = true;
            }
            return sp;
        }

        // the hovered card of ours: the flame in the room left before its price's number,
        // as high as the price's text (the game's own Collection prices put their currency's icon there, 16 pixels at
        // font size 16: FUN_0046e9e0's "<img src='img://%s' width='16' height='16' vspace='-6' /> %u")
        private function placeHoverVim():void {
            var c:Object = hoverCard;
            var tf:TextField = null;
            try {
                var d:Object = c == null ? null : c.data;
                var ours:Boolean = d != null && uint(d.itemType) == NO_DETAILS_TYPE && String(d.price).indexOf("&nbsp;") == 0;
                tf = ours && c.mcPrice != null ? c.mcPrice.textField as TextField : null;
            } catch (err:Error) {
                tf = null;
            }
            if (tf == null || vimFailed || tf.text == "") {
                if (hoverVim != null && hoverVim.parent != null) {
                    hoverVim.parent.removeChild(hoverVim);
                }
                return;
            }
            if (hoverVim == null) {
                hoverVim = makeVim();
            }
            var holder:DisplayObjectContainer = tf.parent;
            if (hoverVim.parent != holder) {
                holder.addChild(hoverVim);
            }
            var h:Number = tf.textHeight;
            var size:Number = Math.max(8, Math.round(h * 0.8));
            // where the number starts in the centred line: the spaces' width is the line's width less the number's
            // alone (measured once per card and price, the field's text put back in the same frame)
            var price:String = String(c.data.price);
            if (vimMeasured[c] !== price) {
                var whole:Number = tf.textWidth;
                tf.htmlText = price.substr(VIM_GAP.length);
                vimSpaces[c] = Math.max(0, whole - tf.textWidth);
                tf.htmlText = price;
                vimMeasured[c] = price;
            }
            var start:Number = tf.x + Math.max(0, (tf.width - tf.textWidth) / 2) + Number(vimSpaces[c]);
            var x:Number = start - 2 - size * VIM_RIGHT; // the flame's drawn part ends 2 units before the number
            hoverVim.scaleX = hoverVim.scaleY = size / VIM_SIZE;
            hoverVim.x = Math.round(x);
            hoverVim.y = Math.round(tf.y + 2 + (h - size) / 2);
            var note:String = "price flame " + size + " at " + hoverVim.x + "," + hoverVim.y + " text " +
                Math.round(tf.textWidth) + "x" + Math.round(h) + " room " + Math.round(Number(vimSpaces[c]));
            if (note != vimNote) {
                vimNote = note;
                say(note);
            }
        }

        // The results view sizes its scroll bar for its own grid of full cards (ResultsView._UpdateCardPositions: the
        // last row's bottom plus half a card, less the view's height), for its own placing of the cards,
        // not our grid's (COLLITEMS2 had small cards; the spells' tall cards take two rows). While our cards show, the scroll bar gets our
        // range (the same rule on our grid) each
        // time the view may have set its own, and the wheel scrolls within it (the view's own wheel handler keeps its
        // range, which the class can't reach).
        private function fixScroll():void {
            var results:Object = screen.mResultsView;
            if (results == null) {
                return;
            }
            var box:Object = results.resultsRootBox;
            if (box != null && box != wheelBox) {
                wheelBox = box;
                box.addEventListener(MouseEvent.MOUSE_WHEEL, onWheel, false, 1000, true);
            }
            if (ourResults == null || results.viewData !== ourResults || isNaN(gridTop) || !scrollDirty) {
                return;
            }
            var sb:Object = findScrollBar(results);
            if (sb == null) {
                return;
            }
            var rows:int = Math.ceil(ourResults.length / columns(gridWidth()));
            var height:Number = gridTop + rows * (cellH + PAD) - PAD + CARD_H * 0.5;
            var viewH:Number = Number(results.bounds.height);
            scrollMax = isNaN(viewH) ? 0 : Math.max(0, Math.round(height - viewH));
            sb.setScrollProperties(SCROLL_PAGE, 0, scrollMax, SCROLL_PAGE);
            sb.visible = scrollMax > 0;
            if (sb.position > scrollMax) {
                sb.position = scrollMax;
            }
            scrollDirty = false;
            say("scroll: " + rows + " rows, " + Math.round(height) + " high, view " + Math.round(viewH) + ", max " + scrollMax);
        }

        private function onWheel(e:MouseEvent):void {
            try {
                if (screen == null || ourResults == null || screen.mResultsView.viewData !== ourResults) {
                    return;
                }
                var sb:Object = findScrollBar(screen.mResultsView);
                if (sb == null) {
                    return;
                }
                e.stopImmediatePropagation();
                // as the view's own wheel (_OnResultsMouseWheel, _AnimScroll): the target moves a page a notch from
                // the running tween's target, and the bar's position is tweened there in 0.001 s a unit
                var pos:Number = Number(sb.position);
                var from:Number = wheelTween != null && getTimer() < wheelEnd ? wheelTarget : pos;
                var target:Number = Math.max(0, Math.min(scrollMax, from - e.delta * SCROLL_PAGE));
                stopWheelTween();
                wheelTarget = target;
                if (target == pos) {
                    return;
                }
                var time:Number = Math.abs(target - pos) * SCROLL_TIME;
                var tween:Object = null;
                try {
                    tween = getDefinitionByName(TWEEN_CLASS);
                } catch (err:Error) {
                }
                if (tween == null) {
                    sb.position = target;
                    return;
                }
                wheelTween = tween.to(sb, time, {position: target});
                wheelEnd = getTimer() + Math.ceil(time * 1000) + 50;
                say("wheel: " + Math.round(pos) + " to " + Math.round(target) + " in " + time + " s");
            } catch (err:Error) {
            }
        }

        private function stopWheelTween():void {
            if (wheelTween != null) {
                try {
                    wheelTween.kill();
                } catch (err:Error) {
                }
                wheelTween = null;
            }
        }

        // the results view's scroll bar (its own member is protected): the scroll bar under the view's Sprite
        private function findScrollBar(results:Object):Object {
            if (scrollBar != null && scrollBar.stage != null) {
                return scrollBar;
            }
            scrollBar = null;
            for each (var r:Object in [results.displayContainer, results.resultsRootBox]) {
                var out:Array = [];
                collectScrollBars(r as DisplayObject, 0, out);
                if (out.length > 0) {
                    scrollBar = out[0];
                    break;
                }
            }
            return scrollBar;
        }

        private function collectScrollBars(o:DisplayObject, depth:int, out:Array):void {
            if (o == null || depth > 12 || out.length > 0) {
                return;
            }
            if ("setScrollProperties" in o && "position" in o) {
                out.push(o);
                return;
            }
            if ("OnCardRenderReady" in o) {
                return; // a card
            }
            var c:DisplayObjectContainer = o as DisplayObjectContainer;
            if (c == null) {
                return;
            }
            for (var i:int = 0; i < c.numChildren; i++) {
                try {
                    collectScrollBars(c.getChildAt(i), depth + 1, out);
                } catch (err:Error) {
                }
            }
        }

        private function collectCards(o:DisplayObject, depth:int, out:Array):void {
            if (o == null || depth > MAX_DEPTH) {
                return;
            }
            if ("OnCardRenderReady" in o && "Init" in o) {
                out.push(o);
                return;
            }
            var c:DisplayObjectContainer = o as DisplayObjectContainer;
            if (c == null) {
                return;
            }
            var n:int = 0;
            try {
                n = c.numChildren;
            } catch (err:Error) {
                return;
            }
            for (var i:int = 0; i < n; i++) {
                try {
                    collectCards(c.getChildAt(i), depth + 1, out);
                } catch (err:Error) {
                }
            }
        }

        // the game's results again (the player picked one of its options): its details listener back, ours unselected
        private function watchResults():void {
            if (ourResults == null) {
                return;
            }
            var results:Object = screen.mResultsView;
            if (results == null || results.viewData === ourResults) {
                return;
            }
            say("the game's results again");
            gameSorts();
            if (query != "") {
                // our search's text is no search of the game's (C++ never saw it)
                query = "";
                greyState++;
                try {
                    screen.SetSearchText("");
                } catch (err:Error) {
                }
            }
            if (detailsOff) {
                results.signalRequestDetails.remove(onCard);
                results.signalRequestDetails.add(screen.OpenDetailsByItemData);
                detailsOff = false;
            }
            ourResults = null;
            landing = false;
            if (button != null) {
                button.ClearOptionStates();
            }
            kindsOn = {};
            filtersOn = {};
            greyState++;
            if (spellButton != null) {
                spellButton.ClearOptionStates();
            }
        }

        // ---- search, sort and filters for our items (COLLITEMS4) --------------------------------------------------

        // our cards are the results view's data (not our empty set under the landing page: the search is the game's)
        private function oursShown():Boolean {
            return screen != null && ourResults != null && !landing && screen.mResultsView != null &&
                screen.mResultsView.viewData === ourResults;
        }

        // the Collection's search field (CollectionView.searchInput, an InputTermField): its CHANGE and submit events
        // first while our cards show (priority 1000, stopped there: C++ would search its own list and replace ours)
        private function hookSearch():void {
            var field:Object = screen.searchInput;
            if (field == null || field == searchField) {
                return;
            }
            searchField = field;
            field.addEventListener(Event.CHANGE, onSearchChange, false, 1000, true);
            field.addEventListener(SEARCH_SUBMIT, onSearchSubmit, false, 1000, true);
        }

        private function onSearchChange(e:Event):void {
            try {
                if (!oursShown()) {
                    return;
                }
                e.stopImmediatePropagation();
                var q:String = String(searchField.text).toLowerCase().replace(/^\s+|\s+$/g, "");
                if (q != query) {
                    query = q;
                    greyState++;
                    say("search: '" + q + "'");
                }
            } catch (err:Error) {
            }
        }

        private function onSearchSubmit(e:Event):void {
            if (oursShown()) {
                e.stopImmediatePropagation(); // no search term chip: the text stays the search
            }
        }

        // an item matches the search when each word is in its name, kind, group, stat lines or passive names
        private function matches(it:Object):Boolean {
            if (query == "") {
                return true;
            }
            var hay:String = it.hay;
            if (hay == null) {
                hay = (it.title + " " + kindLabel(it.kind) + " " + it.group).toLowerCase();
                for each (var s:Array in it.stats) {
                    hay += " " + runsText(s).toLowerCase();
                }
                for each (var p:Object in it.passives) {
                    hay += " " + String(p.title).toLowerCase();
                }
                it.hay = hay;
            }
            for each (var w:String in query.split(/\s+/)) {
                if (w != "" && hay.indexOf(w) < 0) {
                    return false;
                }
            }
            return true;
        }

        private static function runsText(runs:Array):String {
            var out:String = "";
            for (var i:int = 0; i + 1 < runs.length; i += 2) {
                out += runs[i] == "n" ? " " : String(runs[i + 1]);
            }
            return out;
        }

        // greyed: not matching the search, or lacking one of the checked filter tags (each checked tag must be one of
        // its stats' attributes or tags)
        private function greyed(it:Object):Boolean {
            for (var tag:String in filtersOn) {
                // the item filters are for items only (a spell is greyed by the search alone)
                if (filtersOn[tag] === true && it.kind != "spell" && it.tags.indexOf(tag) < 0) {
                    return true;
                }
            }
            return !matches(it);
        }

        // a card of ours greyed or not (its icon desaturated as the game's IconLoader does it, the card darkened to GREY_DARK),
        // set again only when its item or state changed (cards are pooled)
        private function greyCard(c:Object, it:Object):void {
            var key:String = (it == null ? "" : it.name) + ":" + greyState;
            if (greyKey[c] === key) {
                return;
            }
            greyKey[c] = key;
            var g:Boolean = it != null && greyed(it);
            // the card darkened, not faded: the card's alpha fades each of its parts on its own, and at full size the
            // kind line hidden under the title bar showed through (collitems9_dbg1c); a ColorMatrixFilter's alpha on
            // the card did nothing in Scaleform (collitems9_dbg2c); a colour transform keeps hidden parts hidden
            c.alpha = 1;
            c.transform.colorTransform = g ? new ColorTransform(GREY_DARK, GREY_DARK, GREY_DARK, 1) : new ColorTransform();
            try {
                if (g) {
                    c.mcImage.AddDesaturateFilter();
                } else {
                    c.mcImage.ClearFilters();
                }
            } catch (err:Error) {
            }
        }

        // an item card's info field (inside mcInfo, which the card's timeline moves) placed for the card's hover state
        // (COLLITEMS11): INFO_SHIFT_IDLE below the field's own y on the unhovered card, INFO_SHIFT_OVER on the hovered
        // one, in between by mcInfo's place in the Over and Out animations; a spell's at its own y. Every frame (the
        // animation runs on), a property only (no display object goes into the card: COLLITEMS7).
        private function fitInfo(c:Object, it:Object):void {
            var tf:TextField = null;
            try {
                tf = c.mcInfo.textField as TextField;
            } catch (err:Error) {
            }
            if (tf == null) {
                return;
            }
            if (infoY[c] === undefined) {
                infoY[c] = tf.y;
            }
            var y:Number = Number(infoY[c]);
            if (it != null && it.kind != "spell") {
                var f:Number = (Number(c.mcInfo.y) - INFO_IDLE_Y) / (INFO_OVER_Y - INFO_IDLE_Y);
                f = f < 0 ? 0 : (f > 1 ? 1 : f);
                y += INFO_SHIFT_IDLE + (INFO_SHIFT_OVER - INFO_SHIFT_IDLE) * f;
            }
            if (Math.abs(tf.y - y) > 0.04) {
                tf.y = y;
            }
        }

        // the Sort dropdown (CollectionView.sortDropdown, CLIK): while our cards show it holds our sorts (the game's
        // list, index and row count kept and given back with the game's results), and its pick sorts our cards
        private function hookSort():void {
            var dd:Object = screen.sortDropdown;
            if (dd == null || dd == sortDrop) {
                return;
            }
            sortDrop = dd;
            gameSort = null;
            dd.addEventListener(INDEX_CHANGE, onSortChange, false, 1000, true);
        }

        private function ourSorts():void {
            var dd:Object = sortDrop;
            if (dd == null || gameSort != null) {
                return;
            }
            try {
                var dpClass:Class = getDefinitionByName(DATA_PROVIDER_CLASS) as Class;
                var prefix:String = sortPrefix(dd);
                var list:Array = [];
                for (var i:int = 0; i < SORTS.length; i++) {
                    list.push({label: prefix + SORTS[i], id: i});
                }
                gameSort = {dp: dd.dataProvider, index: dd.selectedIndex, rows: dd.menuRowCount, scroll: dd.scrollBar};
                dd.scrollBar = null;
                dd.dataProvider = new dpClass(list);
                dd.selectedIndex = sortIndex;
                say("sorts: ours (the game's first: '" + labelOf(gameSort.dp, 0) + "')");
            } catch (err:Error) {
                gameSort = null;
                say("sorts failed " + err.errorID + ": " + err.message);
            }
        }

        private function gameSorts():void {
            var dd:Object = sortDrop;
            if (dd == null || gameSort == null) {
                return;
            }
            try {
                dd.dataProvider = gameSort.dp;
                dd.menuRowCount = gameSort.rows;
                dd.scrollBar = gameSort.scroll;
                dd.selectedIndex = gameSort.index;
            } catch (err:Error) {
            }
            gameSort = null;
        }

        // "Sort by: " when the game's own labels start with it (they read "Sort by: Default")
        private static function sortPrefix(dd:Object):String {
            var first:String = labelOf(dd.dataProvider, 0);
            var k:int = first.indexOf(": ");
            return k > 0 && k < 16 ? first.substr(0, k + 2) : "";
        }

        private static function labelOf(dp:Object, i:int):String {
            try {
                if (dp != null && dp.length > i && dp[i] != null) {
                    return String(dp[i].label);
                }
            } catch (err:Error) {
            }
            return "";
        }

        private function onSortChange(e:Event):void {
            try {
                if (!oursShown() || gameSort == null) {
                    return;
                }
                e.stopImmediatePropagation();
                var i:int = int(sortDrop.selectedIndex);
                if (i >= 0 && i < SORTS.length && i != sortIndex) {
                    sortIndex = i;
                    say("sort: " + SORTS[i]);
                    showItems(shownKinds, false, shownGroup);
                }
            } catch (err:Error) {
            }
        }

        // our items in the picked order: the list's (kind, cost, name), by name, by cost up, by cost down
        private function sorted(list:Array):Array {
            var out:Array = list.slice();
            if (sortIndex == 1) {
                out.sort(function (a:Object, b:Object):int {
                    return a.title < b.title ? -1 : a.title > b.title ? 1 : a.order - b.order;
                });
            } else if (sortIndex == 2 || sortIndex == 3) {
                var dir:int = sortIndex == 2 ? 1 : -1;
                out.sort(function (a:Object, b:Object):int {
                    return a.cost != b.cost ? dir * (a.cost - b.cost) : a.order - b.order;
                });
            }
            return out;
        }

        // The menu's scroll range (MainMenuView._UpdatePositioning: its buttons' bottom less its height) counts only
        // the game's categories; ours below them get the same rule, set again whenever the menu set its own
        private function fixMenuScroll():void {
            var menu:Object = screen.mMenuView;
            var last:Object = spellButton != null ? spellButton : button;
            if (menu == null || last == null || buttonScreen != screen) {
                return;
            }
            // the wheel over the whole menu, in the capture phase (the menu's own handler sits on its scroll container,
            // below the menu's Sprite; a listener on our button's parent missed the wheel over the
            // game's categories)
            var canvas:DisplayObject = menu.displayContainer as DisplayObject;
            if (canvas != null && canvas != menuWheelBox) {
                menuWheelBox = canvas;
                canvas.addEventListener(MouseEvent.MOUSE_WHEEL, onMenuWheel, true, 1000, true);
            }
            var bottom:Number = last.y + last.height + 3;
            var h:Number = NaN;
            try {
                h = Number(menu.bounds.height);
            } catch (err:Error) {
            }
            if (isNaN(h)) {
                return;
            }
            var max:Number = Math.max(0, Math.round(bottom - h));
            var sb:Object = menuScrollBar(menu);
            if (sb == null) {
                if (tick % 200 == 0) {
                    say("menu scroll bar not found (bottom " + Math.round(bottom) + ", height " + Math.round(h) + ")");
                }
                return;
            }
            // the menu sets its own range when a category opens or closes (and its scroll bar's thumb with it)
            var sig:String = max + ":" + Math.round(Number(menu.bounds.top)) + ":" + menuSig(menu);
            // (and again every half second: its last setting can come after ours in a frame, and the bar's range
            // can't be read back)
            var changed:Boolean = sig != menuScrollSig;
            if (!changed && tick % 20 != 0) {
                return;
            }
            menuScrollSig = sig;
            menuScrollMax = max;
            sb.setScrollProperties(MENU_PAGE, 0, max, MENU_PAGE);
            if (sb.position > max) {
                sb.position = max;
            }
            if (changed && max > 0) {
                say("menu scroll: max " + max + " (bottom " + Math.round(bottom) + ", height " + Math.round(h) + ")");
            }
        }

        // the game's categories' heights (each changes while it opens or closes)
        private function menuSig(menu:Object):String {
            var s:String = "";
            for each (var b:Object in menuButtons(menu)) {
                s += Math.round(b.height) + ",";
            }
            return s;
        }

        private function menuScrollBar(menu:Object):Object {
            if (menuBar != null && menuBar.stage != null) {
                return menuBar;
            }
            var out:Array = [];
            collectScrollBars(menu.displayContainer as DisplayObject, 0, out);
            menuBar = out.length > 0 ? out[0] : null;
            return menuBar;
        }

        private function onMenuWheel(e:MouseEvent):void {
            try {
                var sb:Object = menuScrollBar(screen.mMenuView);
                if (sb == null) {
                    return;
                }
                e.stopImmediatePropagation();
                sb.position = Math.max(0, Math.min(menuScrollMax, Number(sb.position) - e.delta * MENU_PAGE));
            } catch (err:Error) {
            }
        }

        // ---- the details panel (COLLITEMS3) ------------------------------------------------------------------------

        // the Collection's art for the panel, asked once per screen from the menu's asset provider (the same call as for
        // our category button; the ids are the collection manifest's)
        private function askAssets(forScreen:Object):void {
            if (assetsScreen == forScreen) {
                return;
            }
            assetsScreen = forScreen;
            artProxies = {};
            var menu:Object = forScreen.mMenuView;
            for each (var id:String in ART_IDS) {
                askArt(menu, id, forScreen);
            }
        }

        private function askArt(menu:Object, id:String, forScreen:Object):void {
            menu.createAssetSet(id, [], "proxy", 1, function (set:Array):void {
                try {
                    if (forScreen == screen && set != null && set.length > 0 && set[0].assetInstance != null) {
                        artProxies[id] = set[0];
                        if (openItem != null) {
                            layoutPanel();
                        }
                    }
                } catch (err:Error) {
                    say("art " + id + " failed " + err.errorID + ": " + err.message);
                }
            }, true);
        }

        // A route tile as the in-match shop draws an item in its tier lists (ItemShopRenderer; SHOP_TILE_*), in the
        // shop's units (the tile is scaled to the shop's size on screen): the band, the selected tile's frame, the icon,
        // the name in its tier's colour and the cost (the vim flame, then the number in white with the shop's gold
        // glow). The class's own Sprite, in no card; no hover change (the shop's band lightens on hover, the user
        // wanted none here); a click opens its item.
        private function makeTile():Object {
            var sp:Sprite = new Sprite();
            sp.mouseChildren = false;
            var band:Shape = new Shape();
            band.graphics.beginFill(0, TILE_BAND_ALPHA);
            band.graphics.drawRect(0, 6, SHOP_TILE_W, 40);
            band.graphics.endFill();
            sp.addChild(band);
            var frame:Shape = new Shape();
            frame.graphics.lineStyle(3, TILE_FRAME_COLOR, 1, true);
            frame.graphics.drawRoundRect(-0.5, 5.5, SHOP_TILE_W + 1, 41, 8, 8);
            frame.graphics.lineStyle(1, 0x2A2A1E, 1, true);
            frame.graphics.drawRoundRect(1.5, 7.5, SHOP_TILE_W - 3, 37, 4, 4);
            frame.visible = false;
            sp.addChild(frame);
            var ld:Loader = new Loader();
            ld.x = 2;
            ld.y = 8;
            sp.addChild(ld);
            // the front end's $Header is the shop tiles' Friz Quadrata ($Body is a plainer face here)
            var name:TextField = makeText(SHOP_TILE_TEXT, 0xFFFFFF, "$Header");
            var cost:TextField = makeText(SHOP_TILE_TEXT, 0xFFFFFF, "$Header");
            for each (var t:TextField in [name, cost]) {
                t.multiline = t.wordWrap = false;
                t.width = SHOP_TILE_W - 38 + 40; // a long name runs past the band as in the shop (Inevitability)
                t.height = 22;
                t.x = 38;
                sp.addChild(t);
            }
            name.y = 4;
            cost.y = 24;
            cost.filters = [new GlowFilter(GOLD_COLOR, 1, 3, 3, 1)];
            var vim:Sprite = makeVim();
            vim.scaleX = vim.scaleY = SHOP_TILE_TEXT / VIM_SIZE;
            vim.x = 39;
            vim.y = 28;
            sp.addChild(vim);
            var tile:Object = {sprite: sp, frame: frame, loader: ld, name: name, cost: cost, vim: vim, icon: "", item: null};
            ld.contentLoaderInfo.addEventListener(Event.COMPLETE, function (e:Event):void {
                var bmp:Bitmap = ld.content as Bitmap;
                if (bmp != null) {
                    bmp.smoothing = true;
                }
                // the shop's box (kIconSizeStretch: the texture drawn at the box's size, smaller than it)
                ld.width = ld.height = SHOP_ICON;
            }, false, 0, false);
            ld.contentLoaderInfo.addEventListener(IOErrorEvent.IO_ERROR, function (e:Event):void {}, false, 0, false);
            sp.addEventListener(MouseEvent.CLICK, function (e:MouseEvent):void {
                try {
                    if (tile.item != null && tile.item != openItem) {
                        openPanel(tile.item);
                    }
                } catch (err:Error) {
                    say("route tile failed " + err.errorID + ": " + err.message);
                }
            }, false, 0, false);
            return tile;
        }

        // a tile for an item (selected: the opened item)
        private function setTile(tile:Object, it:Object, selected:Boolean):void {
            tile.item = it;
            tile.frame.visible = selected;
            setHtml(tile.name, '<font color="' + itemColour(it) + '">' + esc(it.title) + "</font>");
            var flame:Boolean = it.cost > 0 && !vimFailed;
            tile.vim.visible = flame;
            tile.cost.x = flame ? 38 + SHOP_TILE_TEXT + 1 : 38;
            setHtml(tile.cost, it.cost > 0 ? esc(String(it.cost) + (flame ? "" : " vim")) : "");
            if (tile.icon != it.icon) {
                tile.icon = it.icon;
                var ld:Loader = tile.loader;
                try {
                    ld.unload();
                    if (it.icon != null && it.icon != "") {
                        ld.load(new URLRequest("img://" + it.icon)); // as the game's IconLoader asks for a texture
                    }
                } catch (err:Error) {
                }
            }
        }

        // an asset of the provider on one of our layers (its proxy's canvas, as the menu places its buttons)
        private function adopt(proxy:Object, layer:Sprite):DisplayObject {
            var a:DisplayObject = proxy.assetInstance as DisplayObject;
            if (a == null) {
                return null;
            }
            if (artAdopted[a] !== layer) {
                try {
                    proxy.canvas = layer;
                } catch (err:Error) {
                }
                layer.addChild(a);
                artAdopted[a] = layer;
            }
            return a;
        }

        private function openPanel(it:Object):void {
            if (screen == null) {
                return;
            }
            var first:Boolean = openItem == null;
            openItem = it;
            ensureHost();
            layoutPanel();
            if (first) {
                playSound("COLLECTION_CARD_OPEN");
                gameScrim(true);
            }
        }

        // the screen's own scrim (its details screen shows it the same way: CollectionView.OpenDetailsByItemData), under
        // our panel; ours, on top, takes the clicks
        private function gameScrim(show:Boolean):void {
            try {
                if (screen != null && screen.scrimProxy != null) {
                    screen.scrimProxy.visible = show;
                    screen.scrimProxy.validateNow();
                }
            } catch (err:Error) {
            }
        }

        private function closePanel():void {
            if (openItem == null) {
                return;
            }
            openItem = null;
            if (host != null && host.parent != null) {
                host.parent.removeChild(host);
            }
            gameScrim(false);
            playSound("COLLECTION_CARD_CLOSE");
        }

        // the screen is gone: the panel with it (its art was the screen's provider's)
        private function dropPanel():void {
            openItem = null;
            if (host != null && host.parent != null) {
                host.parent.removeChild(host);
            }
            host = null;
            hostScreen = null;
            debugTf = null;
            artProxies = {};
            routeTiles = [];
            assetsScreen = null;
        }

        private function keepPanelOnTop():void {
            if (host == null || openItem == null) {
                return;
            }
            if (hostScreen != screen) {
                dropPanel();
                return;
            }
            var p:DisplayObjectContainer = host.parent;
            var top:int = p == null ? 0 : p.numChildren - (debugTf != null && debugTf.parent == p ? 2 : 1);
            if (p != null && p.getChildIndex(host) < top) {
                p.setChildIndex(host, top);
            }
            sizePanelIcon();
        }

        // the panel's icon at the size of the shop's Current Selection in pixels, centred in its frame
        private function sizePanelIcon():void {
            if (iconFrame == null || iconLoader == null || iconFrame.stage == null) {
                return;
            }
            var px:Number = shopPixels(SHOP_SELECTED_ICON, iconFrame);
            var k:Number = pixelScale(iconFrame);
            if (isNaN(px) || !(k > 0.01)) {
                return;
            }
            var u:Number = px / k;
            if (iconLoader.content != null && (Math.abs(iconLoader.width - u) > 0.25 || Math.abs(iconLoader.height - u) > 0.25)) {
                iconLoader.width = u;
                iconLoader.height = u;
                iconLoader.x = -u / 2;
                iconLoader.y = -u / 2;
            }
            if (isNaN(iconSize) || Math.abs(iconSize - u) > 0.25) {
                iconSize = u;
                var fg:Graphics = iconFrame.graphics;
                fg.clear();
                fg.lineStyle(2, FRAME_COLOR);
                fg.beginFill(0, 0.6);
                fg.drawRect(-u / 2 - 2, -u / 2 - 2, u + 4, u + 4);
                fg.endFill();
                say("panel icon " + px + " px (" + Math.round(u) + " units)");
            }
        }

        // the game's own sounds of its details screen (BaseCollectionItemDetailView: SoundMan, InteractEvents)
        private function playSound(name:String):void {
            try {
                if (soundMan == null) {
                    var sm:Class = getDefinitionByName("com.waystone.sound.SoundMan") as Class;
                    soundMan = new sm();
                }
                var ev:Object = getDefinitionByName("maxis.constants.InteractEvents");
                soundMan.playSounds(ev[name]);
            } catch (err:Error) {
            }
        }

        private static function makeText(size:int, color:uint, font:String = "$Body", align:String = "left"):TextField {
            var t:TextField = new TextField();
            t.embedFonts = true;
            t.selectable = false;
            t.mouseEnabled = false;
            t.multiline = true;
            t.wordWrap = true;
            var f:TextFormat = new TextFormat(font, size, color);
            f.align = align;
            t.defaultTextFormat = f;
            var c:String = color.toString(16);
            while (c.length < 6) {
                c = "0" + c;
            }
            textSpecs[t] = {font: font, size: size, color: "#" + c, align: align};
            return t;
        }

        // HTML in a text field in its own font, size, colour and alignment (makeText's; size overrides the size):
        // Scaleform's htmlText starts from its own default font (a serif at 12, black), not the field's
        // defaultTextFormat, and a field's defaultTextFormat reads back as that font after an htmlText
        // (seen live)
        private static function setHtml(t:TextField, inner:String, size:int = 0):void {
            var s:Object = textSpecs[t];
            if (s == null) {
                t.htmlText = inner;
                return;
            }
            t.htmlText = '<p align="' + s.align + '"><font face="' + s.font + '" size="' + (size > 0 ? size : s.size) +
                '" color="' + s.color + '">' + inner + "</font></p>";
        }

        // the host Sprite on top of the screen's: the dimmed screen (a click on it closes) and the panel's layers
        private function ensureHost():void {
            var container:DisplayObjectContainer = screen.displayContainer as DisplayObjectContainer;
            if (container == null) {
                return;
            }
            if (host == null || hostScreen != screen) {
                hostScreen = screen;
                host = new Sprite();
                var scrim:Sprite = new Sprite(); // drawn over the stage in layoutPanel
                hostScrim = scrim;
                scrim.addEventListener(MouseEvent.MOUSE_DOWN, function (e:MouseEvent):void {
                    if (e.target == scrim) {
                        closePanel();
                    }
                });
                host.addChild(scrim);
                panel = new Sprite();
                panelVim = null;
                host.addChild(panel);
                fallbackLayer = new Sprite();
                artLayer = new Sprite();
                lineLayer = new Sprite();
                tileLayer = new Sprite();
                routeTiles = [];
                panel.addChild(fallbackLayer);
                panel.addChild(artLayer);
                artLayer.mouseChildren = artLayer.mouseEnabled = false;
                iconFrame = new Sprite();
                iconSize = NaN;
                iconLoader = new Loader();
                iconFrame.addChild(iconLoader);
                panel.addChild(iconFrame);
                titleTf = makeText(26, 0xFFFFFF, "$Header", "center");
                titleTf.filters = [new GlowFilter(0, 1, 3, 3)];
                kindTf = makeText(16, 0xCCCCCC);
                costTf = makeText(18, GOLD_COLOR);
                costTf.filters = [new GlowFilter(0, 1, 3, 3)];
                textTf = makeText(16, TEXT_COLOR);
                hintTf = makeText(13, DIM_COLOR, "$Body", "center");
                emptyTf = makeText(13, DIM_COLOR, "$Body", "center");
                for each (var t:TextField in [titleTf, kindTf, costTf, textTf, hintTf, emptyTf]) {
                    panel.addChild(t);
                }
                headerTfs = [];
                for (var i:int = 0; i < 3; i++) {
                    var h:TextField = makeText(17, 0xFFFFFF, "$Header", "center");
                    h.filters = [new GlowFilter(0, 1, 3, 3)];
                    headerTfs.push(h);
                    panel.addChild(h);
                }
                panel.addChild(lineLayer);
                lineLayer.mouseChildren = lineLayer.mouseEnabled = false;
                panel.addChild(tileLayer);
                closeDrawn = new Sprite();
                closeDrawn.buttonMode = true;
                var g:Graphics = closeDrawn.graphics;
                g.beginFill(0, 0.6);
                g.lineStyle(1, FRAME_COLOR);
                g.drawRect(0, 0, 26, 26);
                g.endFill();
                g.lineStyle(3, 0xD8C8A0);
                g.moveTo(7, 7);
                g.lineTo(19, 19);
                g.moveTo(19, 7);
                g.lineTo(7, 19);
                closeDrawn.addEventListener(MouseEvent.CLICK, function (e:MouseEvent):void {
                    closePanel();
                });
                panel.addChild(closeDrawn);
                artAdopted = new Dictionary(true);
            }
            if (host.parent != container) {
                container.addChild(host);
            }
            keepPanelOnTop();
        }

        // the main view's area (the screen's box right of the menu) in the host's coordinates
        private function viewArea():Rectangle {
            try {
                var box:DisplayObject = screen.mcMainViewAreaBox as DisplayObject;
                var r:Rectangle = screen.mcMainViewAreaBox.contentRenderingArea as Rectangle;
                if (box != null && r != null && r.width > 100 && r.height > 100) {
                    var a:Point = host.globalToLocal(box.localToGlobal(new Point(r.x, r.y)));
                    var b:Point = host.globalToLocal(box.localToGlobal(new Point(r.right, r.bottom)));
                    return new Rectangle(a.x, a.y, b.x - a.x, b.y - a.y);
                }
            } catch (err:Error) {
            }
            var tl:Point = host.globalToLocal(new Point(0, 0));
            var br:Point = host.globalToLocal(new Point(host.stage.stageWidth, host.stage.stageHeight));
            return new Rectangle(tl.x + (br.x - tl.x) * 0.15, tl.y, (br.x - tl.x) * 0.85, br.y - tl.y);
        }

        private static function drawBox(g:Graphics, x:Number, y:Number, w:Number, h:Number, fill:uint):void {
            g.lineStyle(2, FRAME_COLOR, 1);
            g.beginFill(fill, 0.97);
            g.drawRect(x, y, w, h);
            g.endFill();
        }

        // an art piece of the Collection's, sized to a box of the panel (or nothing yet)
        private function placeArt(id:String, x:Number, y:Number, w:Number, h:Number, show:Boolean):DisplayObject {
            var proxy:Object = artProxies[id];
            if (proxy == null) {
                return null;
            }
            var a:DisplayObject = adopt(proxy, artLayer);
            if (a != null) {
                a.visible = show;
                if (show) {
                    a.x = x;
                    a.y = y;
                    a.width = w;
                    a.height = h;
                }
            }
            return a;
        }

        private function layoutPanel():void {
            var it:Object = openItem;
            if (it == null || host == null || panel == null) {
                return;
            }
            var cols:Array = routeColumns(it);
            var w:Number = cols != null ? PANEL_W : LEFT_W;
            // the close button: the details' own (CloseButton001) once it is there
            var closeArt:Object = artProxies["detailsCloseButton"];
            var cb:DisplayObject = closeArt != null ? adopt(closeArt, panel) : null;
            if (cb != null) {
                if (!cb.hasEventListener(MouseEvent.CLICK)) {
                    cb.addEventListener(MouseEvent.CLICK, function (e:MouseEvent):void {
                        closePanel();
                    });
                }
                cb.x = w - cb.width - 14;
                cb.y = 12;
                closeDrawn.visible = false;
                panel.setChildIndex(cb, panel.numChildren - 1);
            } else {
                closeDrawn.x = w - 26 - 14;
                closeDrawn.y = 12;
                closeDrawn.visible = true;
            }

            // the item: name in its tier's colour, the icon, kind and cost, the texts
            var colour:String = itemColour(it);
            setHtml(titleTf, '<font color="' + colour + '">' + esc(it.title) + "</font>");
            titleTf.x = 60;
            titleTf.y = 22;
            titleTf.width = LEFT_W - 120;
            titleTf.height = 40;
            // the icon's frame centred in the 96-unit box left of the kind and cost (sizePanelIcon draws it)
            iconFrame.x = 32 + 48;
            iconFrame.y = 74 + 48;
            loadIcon(it.icon);
            kindTf.x = 146;
            kindTf.y = 86;
            kindTf.width = LEFT_W - 146 - 28;
            kindTf.height = 26;
            setHtml(kindTf, esc(kindLabel(it.kind) + (it.group != "" ? " / " + it.group : "")));
            // the cost in vim: the shop's flame as high as the text's size (18), then the number; or "<n> vim"
            if (panelVim == null) {
                panelVim = makeVim();
                panel.addChild(panelVim);
            }
            var flame:Boolean = it.cost > 0 && !vimFailed;
            costTf.x = flame ? Math.round(146 + COST_SIZE * VIM_RIGHT + 1) : 146; // the text 3 units after the flame
            costTf.y = 118;
            costTf.width = kindTf.width;
            costTf.height = 30;
            setHtml(costTf, it.cost > 0 ? esc(thousands(it.cost) + (flame ? "" : " vim")) : "");
            panelVim.visible = flame;
            panelVim.scaleX = panelVim.scaleY = COST_SIZE / VIM_SIZE;
            panelVim.x = 146;
            panelVim.y = Math.round(costTf.y + 2 + (costTf.textHeight - COST_SIZE) / 2);
            fitText(textTf, detailsHtml(it), 32, 200, LEFT_W - 64, PANEL_H - 200 - 34);
            // without a route (consumables, spells) the panel is as high as its text needs
            var ph:Number = cols != null ? PANEL_H : Math.min(PANEL_H, Math.max(330, Math.ceil(200 + textTf.textHeight + 44)));

            // the frames: the details view's on the left, the related-search box's on the right (as the game's details
            // screen pairs them); drawn boxes underneath until (or unless) the art comes
            var g:Graphics = fallbackLayer.graphics;
            g.clear();
            drawBox(g, 0, 0, LEFT_W, ph, 0x0B1016);
            if (cols != null) {
                drawBox(g, LEFT_W, 0, PANEL_W - LEFT_W, ph, 0x0E141B);
            }
            // the details view's background (a statue under a dome) dimmed, so the texts over it read
            var bg:DisplayObject = placeArt("detailedViewBackground", 0, 0, LEFT_W, ph, true);
            if (bg != null) {
                bg.alpha = 0.45;
            }
            placeArt("detailedViewFrame", 0, 0, LEFT_W, ph, true);
            placeArt("screenBackground001", LEFT_W, 0, PANEL_W - LEFT_W, ph, cols != null);
            placeArt("basicWidgetBorder003", LEFT_W, 0, PANEL_W - LEFT_W, ph, cols != null);

            // the panel over the main view, scaled down to fit a small window (the route's tiles follow from it)
            var area:Rectangle = viewArea();
            var s:Number = Math.min(1, (area.width - 24) / w, (area.height - 24) / PANEL_H);
            drawRoute(it, cols, tileScale(s));
            var lg:Graphics = lineLayer.graphics; // the divider under the item's head (above the art)
            lg.lineStyle(1, FRAME_COLOR, 0.7);
            lg.moveTo(28, 188);
            lg.lineTo(LEFT_W - 28, 188);

            panel.scaleX = panel.scaleY = s;
            panel.x = Math.round(area.x + (area.width - w * s) / 2);
            panel.y = Math.round(area.y + (area.height - ph * s) / 2);
            // the dimmed screen: the stage in the host's coordinates, at the screen's scrim alpha (CollectionView
            // COLLECTION_SCRIM_ALPHA; a 20,000-unit square was not drawn)
            var sr:Rectangle = new Rectangle(area.x - 400, area.y - 300, area.width + 800, area.height + 600);
            try {
                var tl:Point = host.globalToLocal(new Point(0, 0));
                var br:Point = host.globalToLocal(new Point(host.stage.stageWidth, host.stage.stageHeight));
                if (br.x - tl.x > 100 && br.y - tl.y > 100) {
                    sr = new Rectangle(tl.x, tl.y, br.x - tl.x, br.y - tl.y);
                }
            } catch (err:Error) {
            }
            var sg:Graphics = hostScrim.graphics;
            sg.clear();
            sg.beginFill(0, 0.45);
            sg.drawRect(sr.x, sr.y, sr.width, sr.height);
            sg.endFill();
            say("scrim " + Math.round(sr.x) + "," + Math.round(sr.y) + " " + Math.round(sr.width) + "x" + Math.round(sr.height));
            say("panel " + it.name + " at " + panel.x + "," + panel.y + " x" + Math.round(s * 100) / 100 + " area " +
                Math.round(area.x) + "," + Math.round(area.y) + " " + Math.round(area.width) + "x" + Math.round(area.height));
        }

        // a text at the largest size from 16 down to 12 that fits its box
        private static function fitText(t:TextField, html:String, x:Number, y:Number, w:Number, h:Number):void {
            t.x = x;
            t.y = y;
            t.width = w;
            t.height = h;
            for (var size:int = 16; size >= 12; size--) {
                setHtml(t, html, size);
                if (t.textHeight + 6 <= h) {
                    break;
                }
            }
        }

        private function loadIcon(icon:String):void {
            try {
                iconLoader.unload();
                iconLoader.visible = false;
                if (icon == null || icon == "") {
                    return;
                }
                iconLoader.contentLoaderInfo.addEventListener(Event.COMPLETE, onIconLoaded, false, 0, true);
                iconLoader.load(new URLRequest("img://" + icon)); // as the game's IconLoader asks for a texture
            } catch (err:Error) {
            }
        }

        private function onIconLoaded(e:Event):void {
            try {
                var bmp:Bitmap = iconLoader.content as Bitmap;
                if (bmp != null) {
                    bmp.smoothing = true; // as the cards' IconLoader draws its icons
                }
                iconSize = NaN;
                sizePanelIcon();
                iconLoader.visible = true;
            } catch (err:Error) {
            }
        }

        // the route's columns (Basic, Advanced, Legendary) for an item, or null for one without tiers
        private function routeColumns(it:Object):Array {
            var t:int = tierIndex(it.kind);
            if (t < 0) {
                return null;
            }
            var cols:Array = [[], [], []];
            if (t == 0) {
                cols[0] = [it];
                cols[1] = itemsOf(it.intoNames);
            } else if (t == 1) {
                cols[0] = itemsOf(it.fromNames);
                cols[1] = [it];
                cols[2] = itemsOf(it.intoNames);
            } else {
                cols[2] = [it];
                cols[1] = itemsOf(it.fromNames);
                for each (var adv:Object in cols[1]) {
                    for each (var b:Object in itemsOf(adv.fromNames)) {
                        if (cols[0].indexOf(b) < 0) {
                            cols[0].push(b);
                        }
                    }
                }
            }
            return cols;
        }

        private function itemsOf(names:Array):Array {
            var out:Array = [];
            for each (var n:String in names) {
                if (byName[n] != null) {
                    out.push(byName[n]);
                }
            }
            return out;
        }

        private static function tierIndex(kind:String):int {
            return kind == "basic" ? 0 : kind == "advanced" ? 1 : kind == "legendary" ? 2 : -1;
        }

        // the route's tiles' scale in panel units per shop unit: the shop's size on screen (its 1920 x 1080 movie fitted
        // to the window, as shopPixels) for a panel at scale s, but never so large that three columns leave less than
        // ROUTE_MIN_GAP between them (a window wider than about 1700 pixels: the panel stops growing at scale 1)
        private function tileScale(s:Number):Number {
            var inner:Number = PANEL_W - LEFT_W - 44;
            var most:Number = (inner - 2 * ROUTE_MIN_GAP) / (3 * SHOP_TILE_W);
            var k:Number = NaN;
            try {
                var st:Stage = host.stage;
                var hk:Number = pixelScale(host) * s;
                if (st != null && st.stageWidth > 0 && st.stageHeight > 0 && hk > 0.01) {
                    k = Math.min(st.stageWidth / HUD_W, st.stageHeight / HUD_H) / hk;
                }
            } catch (err:Error) {
            }
            return isNaN(k) ? Math.min(0.75, most) : Math.min(k, most);
        }

        // the route: the shop's tiles in three columns (at most TILE_ROWS each, centred as the shop centres a short
        // list), the selected item's tile framed, lines from each item to what it builds into (gold towards the
        // selected item, green from it to its upgrades); a click on a tile opens that item
        private function drawRoute(it:Object, cols:Array, k:Number):void {
            var g:Graphics = lineLayer.graphics;
            g.clear();
            for each (var t0:Object in routeTiles) {
                t0.sprite.visible = false;
                t0.item = null;
            }
            var show:Boolean = cols != null;
            for (var hi:int = 0; hi < 3; hi++) {
                headerTfs[hi].visible = show;
            }
            hintTf.visible = show;
            emptyTf.visible = false;
            if (!show) {
                return;
            }
            var tw:Number = SHOP_TILE_W * k, row:Number = SHOP_TILE_ROW * k;
            var inner:Number = PANEL_W - LEFT_W - 44;
            var gap:Number = (inner - 3 * tw) / 2;
            var labels:Array = ["Basic", "Advanced", "Legendary"];
            var kinds:Array = ["basic", "advanced", "legendary"];
            var colX:Array = [];
            for (var c:int = 0; c < 3; c++) {
                colX.push(LEFT_W + 22 + c * (tw + gap));
                var h:TextField = headerTfs[c];
                setHtml(h, '<font color="' + TIER_COLOR[kinds[c]] + '">' + labels[c] + "</font>");
                h.x = colX[c] - 30;
                h.y = 50;
                h.width = tw + 60;
                h.height = 28;
            }
            setHtml(hintTf, "Click an item to see its details.");
            hintTf.x = LEFT_W + 20;
            hintTf.y = ROUTE_BOTTOM + 8;
            hintTf.width = PANEL_W - LEFT_W - 40;
            hintTf.height = 22;
            // the tiles: a tile's band is 40 of its 52-unit row, from 6; its middle is where the lines meet it
            var mid:Object = {}; // column and item name -> the band's middle height
            var n:int = 0;
            for (c = 0; c < 3; c++) {
                var list:Array = cols[c];
                var rows:int = Math.min(list.length, TILE_ROWS);
                var y0:Number = ROUTE_TOP + Math.max(0, (ROUTE_BOTTOM - ROUTE_TOP - rows * row) / 2);
                for (var j:int = 0; j < rows; j++) {
                    var item:Object = list[j];
                    while (n >= routeTiles.length) {
                        routeTiles.push(makeTile());
                    }
                    var tile:Object = routeTiles[n++];
                    var sp:Sprite = tile.sprite;
                    if (sp.parent != tileLayer) {
                        tileLayer.addChild(sp);
                    }
                    setTile(tile, item, item == it);
                    sp.scaleX = sp.scaleY = k;
                    sp.x = Math.round(colX[c]);
                    sp.y = Math.round(y0 + j * row);
                    sp.visible = true;
                    mid[c + ":" + item.name] = sp.y + 26 * k;
                }
                if (list.length == 0 && c == 2) {
                    setHtml(emptyTf, "Open an advanced item to see the legendary items it builds into.");
                    emptyTf.x = colX[c] - 10;
                    emptyTf.width = tw + 20;
                    emptyTf.height = 90;
                    emptyTf.y = (ROUTE_TOP + ROUTE_BOTTOM) / 2 - 30;
                    emptyTf.visible = true;
                }
            }
            // the lines: from each item in a column to the items of the next column it builds into, band to band
            for (c = 0; c < 2; c++) {
                for each (var a:Object in cols[c]) {
                    for each (var b:Object in cols[c + 1]) {
                        if (a.intoNames.indexOf(b.name) < 0 && b.fromNames.indexOf(a.name) < 0) {
                            continue;
                        }
                        var ya:Object = mid[c + ":" + a.name], yb:Object = mid[(c + 1) + ":" + b.name];
                        if (ya != null && yb != null) {
                            link(g, colX[c] + tw + 2, Number(ya), colX[c + 1] - 2, Number(yb), a == it ? UP_PATH : SET_PATH);
                        }
                    }
                }
            }
            say("route: " + n + " tiles at x" + Math.round(k * 1000) / 1000 + " (tile " + Math.round(tw) + " units)");
        }

        // a line as the shop draws its route: out of the left tile, down or up in the gap's middle, into the right
        // tile with an arrow head
        private static function link(g:Graphics, x1:Number, y1:Number, x2:Number, y2:Number, colour:uint):void {
            var mx:Number = Math.round((x1 + x2) / 2);
            g.lineStyle(3, colour, 1);
            g.moveTo(x1, y1);
            g.lineTo(mx, y1);
            g.lineTo(mx, y2);
            g.lineTo(x2 - 7, y2);
            g.lineStyle(0, colour, 0);
            g.beginFill(colour, 1);
            g.moveTo(x2 - 9, y2 - 6);
            g.lineTo(x2, y2);
            g.lineTo(x2 - 9, y2 + 6);
            g.lineTo(x2 - 9, y2 - 6);
            g.endFill();
        }

        private static function itemColour(it:Object):String {
            var c:Object = TIER_COLOR[it.kind];
            return c != null ? String(c) : OTHER_COLOR;
        }

        private static function thousands(n:int):String {
            var s:String = String(n), out:String = "";
            while (s.length > 3) {
                out = "," + s.substr(s.length - 3) + out;
                s = s.substr(0, s.length - 3);
            }
            return s + out;
        }

        private static function esc(s:String):String {
            return String(s).split("&").join("&amp;").split("<").join("&lt;").split(">").join("&gt;");
        }

        // text runs (colour, text, ...) as HTML: "#RRGGBB" a coloured run, "" plain, "n" a line break
        private static function runsHtml(runs:Array):String {
            var out:String = "";
            for (var i:int = 0; i + 1 < runs.length; i += 2) {
                var c:String = String(runs[i]), t:String = String(runs[i + 1]);
                if (c == "n") {
                    out += "<br>";
                } else if (/^#[0-9A-Fa-f]{6}$/.test(c)) {
                    out += '<font color="' + c + '">' + esc(t) + "</font>";
                } else {
                    out += esc(t);
                }
            }
            return out;
        }

        private static function passiveLabel(kind:String):String {
            return kind == "unique" ? "Unique Passive" : kind == "stackable" ? "Stackable Passive" : "Passive";
        }

        // the item's texts as the shop writes them: the stat lines, then each passive ("Unique Passive - Name: text"),
        // the active ("UNIQUE ACTIVE: text"; a spell's text plain) and its cooldown
        private static function detailsHtml(it:Object):String {
            var h:String = "";
            for each (var s:Array in it.stats) {
                h += '<font color="#FFFFFF">' + runsHtml(s) + "</font><br>";
            }
            if (h != "" && (it.passives.length > 0 || it.active != null)) {
                h += "<br>";
            }
            for each (var p:Object in it.passives) {
                h += '<font color="#9999FF">' + passiveLabel(p.kind) + "</font> - ";
                if (p.title != "") {
                    h += '<font color="#FFFF99">' + esc(p.title) + "</font>: ";
                }
                h += runsHtml(p.runs) + "<br><br>";
            }
            var a:Object = it.active;
            if (a != null) {
                if (it.kind != "spell") {
                    h += '<font color="#9999FF">' + (a.kind == "unique" ? "UNIQUE ACTIVE:" : "ACTIVE:") + "</font> ";
                }
                if (a.title != "") {
                    h += '<font color="#FFFF99">' + esc(a.title) + "</font>: ";
                }
                h += runsHtml(a.runs) + "<br>";
                if (a.cooldown > 0) {
                    h += '<font color="#AAAAAA">Cooldown: ' + a.cooldown + " seconds</font><br>";
                }
            }
            return h;
        }

        // the menu's category buttons: its asset movie's linkage class (a subclass), by class or by their methods
        private function collect(o:DisplayObject, depth:int, out:Array):void {
            if (o == null || depth > MAX_DEPTH) {
                return;
            }
            if (getQualifiedClassName(o) == MENU_BUTTON_CLASS || ("SetOptionClass" in o && "SetOnOptionClickedCallback" in o)) {
                out.push(o);
                return;
            }
            var c:DisplayObjectContainer = o as DisplayObjectContainer;
            if (c == null) {
                return;
            }
            var n:int = 0;
            try {
                n = c.numChildren;
            } catch (err:Error) {
                return;
            }
            for (var i:int = 0; i < n; i++) {
                try {
                    collect(c.getChildAt(i), depth + 1, out);
                } catch (err:Error) {
                }
            }
        }
    }
}
