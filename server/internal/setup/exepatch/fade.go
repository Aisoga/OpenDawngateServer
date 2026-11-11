package exepatch

import "bytes"

// The fade at the fog's edge (Options.NoFade leaves it out). The shipped client draws every enemy creature it holds by
// the fog texture's value at its feet: a pale ghost under 0.5, solid above, at once, so a creature the server sends a
// little before its team sees it (the match host's head start, FOGEDGE, removed by FOGEDGE-RM) showed early. With the patch an enemy creature
// (noun creature with fowControlsObjectVisibility, of a team not in GameClient_GetLocalTeamMask, unless the local
// player observes or there is no local player: the front end's shaper models, in the Collection and elsewhere, are
// such creatures too, and without a player their target was 0, so they were never drawn) is drawn while mVisibleToTeam & the local mask (+0x888) is set or its own fog query (+0x860 ->
// +0x5C, the fog texture at its feet, 0-255) is at least 160, clearly lit (the texture fades out over about a second
// where the viewer was, so a lower value kept an enemy drawn behind a viewer walking away); the fog texture knows walls
// and bushes. Its own opacity (+0x868) follows that
// target by the frame's seconds over 0.25 s. FadeStep runs from GameGraphics_UpdateVisibleObject's fogged test, so at
// opacity 0 the creature is skipped as a fogged one is (no model, health bar or name). The code is in place:
// GameGraphics_ApplyCreatureOpacity, which only the fog exit called, becomes the exit's reset (opacity 0, stamped now,
// so an exit fades in) followed by FadeStep; the target's test sits in GameGraphics_UpdateCreatureOpacity's first
// assert block, which only logged; and its front draws a creature FadeStep marked (+0x870 = "FADE") at opacity 1 with
// its normal materials, else with the labsHeroStealth glass at the opacity squared, and any other creature as shipped.
const (
	fadeApply  = 0x0056E750 // GameGraphics_ApplyCreatureOpacity and its padding up to 0x0056E830; FadeStep at 0x0056E76A
	fadeTarget = 0x0056E44D // GameGraphics_UpdateCreatureOpacity's first assert block; FadeTarget at 0x0056E453
	fadeHook   = 0x00576256 // GameGraphics_UpdateVisibleObject: the fogged test
	fadeFront  = 0x0056E4DB // GameGraphics_UpdateCreatureOpacity after its asserts, up to the normal materials
	fadeParam  = 0x0056E706 // the glass material's parameter: as shipped 0.25 * [+0x864] + 0.25, now [+0x864] itself
)

// fadeApplyOrig is ApplyCreatureOpacity as shipped and the 9 bytes of int3 padding after it.
var fadeApplyOrig = mustHex(
	"53568b74240c33c039869008000068391d00000f95c06860f41d0150e84f83320083c40c85c0741c6a005068391d0000" +
		"6860f41d016884061e016a00e84f7e320083c4188b9e9008000085db0f8482000000578b7b7c2b7b78c1ff024f787490" +
		"8b4b788b14b98d04b96a0052e85f2bffff8bf033c085f60f95c068441d00006860f41d0150e8e682320083c41485c074" +
		"1c6a005068441d00006860f41d01687c061e016a00e8e67d320083c41885f67406837e040174074f79a65f5e5bc3d944" +
		"2414518b4e08d91c245651e8e0fbffff83c40c5f5e5bc3cccccccccccccccccc")

// fadeApplyNew is the fog exit's reset, then FadeStep (edi the object, eax its noun, cl its flags, ebx the
// visitor with the frame number at +4): al = 1 when the object is not drawn. The rest stays int3.
var fadeApplyNew = append(mustHex(
	"8b442404"+ // mov eax, dword ptr [esp + 4]: the object
		"83a06808000000"+ // and dword ptr [eax + 0x868], 0: the fade's opacity: 0
		"8b0d38a05901"+ // mov ecx, dword ptr [0x159a038]: GameGraphics
		"8b09"+ // mov ecx, dword ptr [ecx]: its frame number
		"89886c080000"+ // mov dword ptr [eax + 0x86c], ecx: the stamp: stepped this frame
		"c3"+ // ret
		"f6c101"+ // test cl, 1: FadeStep (edi object, eax noun, cl flags, ebx the visitor): fogged?
		"7411"+ // je 0x56e780
		"80b8cd02000000"+ // cmp byte ptr [eax + 0x2cd], 0: fowControlsObjectVisibility
		"7408"+ // je 0x56e780
		"b001"+ // mov al, 1: fogged, as shipped: not drawn
		"c3"+ // ret
		"5e"+ // pop esi
		"59"+ // pop ecx
		"31c0"+ // xor eax, eax
		"c3"+ // ret
		"85c0"+ // test eax, eax: no noun
		"74f9"+ // je 0x56e77d
		"80b8cd02000000"+ // cmp byte ptr [eax + 0x2cd], 0: fowControlsObjectVisibility
		"74f0"+ // je 0x56e77d
		"8138007dc206"+ // cmp dword ptr [eax], 0x6c27d00: not a creature
		"75e8"+ // jne 0x56e77d
		"51"+ // push ecx: the flags, which the caller reads again
		"56"+ // push esi
		"e8d4fcffff"+ // call 0x56e470: LocalViewAsShipped: no local player, or an observer
		"84c0"+ // test al, al: as shipped from here: grass or stealth 0.5
		"75db"+ // jne 0x56e77b
		"6a00"+ // push 0
		"e879e60f00"+ // call 0x66ce20: GameClient_GetLocalTeamMask(0)
		"59"+ // pop ecx
		"0fb6574d"+ // movzx edx, byte ptr [edi + 0x4d]
		"0fa3d0"+ // bt eax, edx: the object's team (+0x4D) in the mask: drawn as shipped
		"72ca"+ // jb 0x56e77b
		"e89dfcffff"+ // call 0x56e453: FadeTarget (0x0056E453): esi = the target
		"e8d5083800"+ // call 0x8ef090: GameSimulator_GetCurrent
		"50"+ // push eax
		"e8ffb13800"+ // call 0x8f99c0: GameTimeManager_GetDeltaSeconds: the frame's seconds
		"d91c24"+ // fstp dword ptr [esp]
		"f30f100424"+ // movss xmm0, dword ptr [esp]
		"59"+ // pop ecx
		"f30f59056cf81b01"+ // mulss xmm0, dword ptr [0x11bf86c]: * 4.0: a whole fade in 0.25 s
		"8b4304"+ // mov eax, dword ptr [ebx + 4]: the visitor's frame number
		"87876c080000"+ // xchg dword ptr [edi + 0x86c], eax: the stamp
		"2b4304"+ // sub eax, dword ptr [ebx + 4]
		"f7d8"+ // neg eax
		"83f801"+ // cmp eax, 1: stepped this frame or the last one?
		"7608"+ // jbe 0x56e7ed
		"f30f1005d4a31b01"+ // movss xmm0, dword ptr [0x11ba3d4]: else (off screen, fogged, new) straight to the target
		"85f6"+ // test esi, esi
		"7508"+ // jne 0x56e7f9
		"f30f590568f81b01"+ // mulss xmm0, dword ptr [0x11bf868]: * -1.0: fading out
		"f30f588768080000"+ // addss xmm0, dword ptr [edi + 0x868]: the fade's opacity
		"0f57c9"+ // xorps xmm1, xmm1
		"f30f5fc1"+ // maxss xmm0, xmm1
		"f30f5d05d4a31b01"+ // minss xmm0, dword ptr [0x11ba3d4]: clamped to 0..1
		"f30f118768080000"+ // movss dword ptr [edi + 0x868], xmm0
		"c7877008000046414445"+ // mov dword ptr [edi + 0x870], 0x45444146: 'FADE': UpdateCreatureOpacity draws it by +0x868
		"0f2fc1"+ // comiss xmm0, xmm1
		"5e"+ // pop esi
		"59"+ // pop ecx
		"0f96c0"+ // setbe al: 0: not drawn
		"c3"), bytes.Repeat([]byte{0xCC}, 5)...) // ret

// fadeTargetOrig is UpdateCreatureOpacity's first assert block as shipped (pGameObject).
var fadeTargetOrig = mustHex(
	"33c085f60f95c068b01c00006860f41d018974241050e85886320083c40c85c0741c6a005068b01c00006860f41d0168" +
		"04f51d016a00e85881320083c418")

// fadeTargetNew keeps the block's store, jumps over FadeTarget (esi = the target from eax, the local team mask)
// and, after it, LocalViewAsShipped (FadeStep's test whether to leave an object as shipped: no local player, as in
// the front end, or an observer); the rest stays int3.
var fadeTargetNew = append(mustHex(
	"89742408"+ // mov dword ptr [esp + 8], esi: what the assert block stored for later
		"eb38"+ // jmp 0x56e48b: over FadeTarget to the second assert
		"228788080000"+ // and al, byte ptr [edi + 0x888]: mVisibleToTeam & the mask
		"0fb6f0"+ // movzx esi, al: the target: 0 hidden, else shown
		"8b8f60080000"+ // mov ecx, dword ptr [edi + 0x860]: the object's fog query
		"e30b"+ // jecxz 0x56e46f
		"81795ca0000000"+ // cmp dword ptr [ecx + 0x5c], 0xa0: the texture (0-255) against 160
		"f5"+ // cmc
		"83d600"+ // adc esi, 0: lit: shown too
		"c3"+ // ret
		"6a00"+ // push 0: LocalViewAsShipped: al = 1 when FadeStep leaves the object as shipped
		"e8b9e80f00"+ // call 0x66cd30: GameClient_GetLocalPlayer(0): the player or 0, no assert
		"59"+ // pop ecx
		"85c0"+ // test eax, eax: no local player (the front end): as shipped
		"7408"+ // je 0x56e484
		"50"+ // push eax
		"e8be3b3a00"+ // call 0x912040: LabsPlayer_IsObserver: observers as shipped
		"59"+ // pop ecx
		"c3"+ // ret
		"b001"+ // mov al, 1
		"c3"), bytes.Repeat([]byte{0xCC}, 4)...) // ret

var fadeHookOrig = mustHex(
	"f6c101740d80b8cd020000000f85e6000000")

var fadeHookNew = mustHex(
	"e80f85ffff" + // call 0x56e76a: FadeStep
		"84c0" + // test al, al: as shipped from here: grass or stealth 0.5
		"0f85eb000000" + // jne 0x57634e: not drawn
		"90" + // nop
		"90" + // nop
		"90" + // nop
		"90" + // nop
		"90") // nop

var fadeFrontOrig = mustHex(
	"e8b00b380050e8dab43800ddd88b86c408000083c40485c0741680b8741e000000750980b8751e0000007404b001eb02" +
		"32c0d9866c080000f30f10442444f30f10155c5b3901d99e70080000d98664080000f30f11866c080000d99e68080000" +
		"84c074030f28c2f30f100d64061e010f2fc8f30f11866408000076030f28c10f2fc257f30f118664080000765e")

var fadeFrontNew = mustHex(
	"8b86c4080000" + // mov eax, dword ptr [esi + 0x8c4]: the blackboard
		"85c0" + // test eax, eax: no noun
		"740b" + // je 0x56e4f0
		"6683b8741e000000" + // cmp word ptr [eax + 0x1e74], 0: mStealthed or mbInGrass
		"0f95c0" + // setne al
		"f30f10442444" + // movss xmm0, dword ptr [esp + 0x44]: the fade as given (the fog texture)
		"f30f10155c5b3901" + // movss xmm2, dword ptr [0x1395b5c]: 0.5
		"81be7008000046414445" + // cmp dword ptr [esi + 0x870], 0x45444146: an enemy creature FadeStep owns?
		"7528" + // jne 0x56e532
		"f30f108668080000" + // movss xmm0, dword ptr [esi + 0x868]: its fade's opacity
		"f30f59c0" + // mulss xmm0, xmm0: squared: the glass grows in evenly to the eye
		"57" + // push edi
		"84c0" + // test al, al: as shipped from here: grass or stealth 0.5
		"7408" + // je 0x56e523
		"f30f5d05ec103401" + // minss xmm0, dword ptr [0x13410ec]: grass or stealth: at most the shipped 0.375
		"0f2f05d4a31b01" + // comiss xmm0, dword ptr [0x11ba3d4]: whole:
		"0f8338000000" + // jae 0x56e568: the normal materials
		"eb29" + // jmp 0x56e55b
		"84c0" + // test al, al: as shipped from here: grass or stealth 0.5
		"7403" + // je 0x56e539
		"0f28c2" + // movaps xmm0, xmm2
		"f30f5f0564061e01" + // maxss xmm0, dword ptr [0x11e0664]: at least 0.25
		"57" + // push edi
		"0f2fc2" + // comiss xmm0, xmm2
		"0f871d000000" + // ja 0x56e568: above 0.5: the normal materials
		"f30f590564061e01" + // mulss xmm0, dword ptr [0x11e0664]: 0.25 * opacity + 0.25
		"f30f580564061e01" + // addss xmm0, dword ptr [0x11e0664]
		"f30f118664080000" + // movss dword ptr [esi + 0x864], xmm0: the glass material's parameter
		"e95e000000") // jmp 0x56e5c6: labsHeroStealth

var fadeParamOrig = mustHex(
	"f30f108e64080000f30f590d64061e01f30f580d64061e01")

var fadeParamNew = append(mustHex("f30f108e64080000"), bytes.Repeat([]byte{0x90}, 16)...) // movss xmm1,[esi+0x864]

// fadeSites are the fade's five places in Patch's order.
var fadeSites = []struct {
	va        uint32
	orig, new []byte
	what      string
}{
	{fadeApply, fadeApplyOrig, fadeApplyNew, "ApplyCreatureOpacity and its padding"},
	{fadeTarget, fadeTargetOrig, fadeTargetNew, "UpdateCreatureOpacity's first assert"},
	{fadeHook, fadeHookOrig, fadeHookNew, "UpdateVisibleObject's fogged test"},
	{fadeFront, fadeFrontOrig, fadeFrontNew, "UpdateCreatureOpacity's fade"},
	{fadeParam, fadeParamOrig, fadeParamNew, "UpdateCreatureOpacity's glass parameter"},
}
