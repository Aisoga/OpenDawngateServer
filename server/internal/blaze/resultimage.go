package blaze

import (
	"errors"
	"fmt"

	"dawngate/server/internal/discordhook"
)

// The result post's image style (M32, DISCORD4b: discord_result_style image): the same embed as the text style
// (title, description, colour, footer, timestamp) without the fields, showing a picture of the scoreboard the server
// draws (ResultPosts.Image: dgserver's internal/scoreboard renderer, from the art beside the content file) as an
// uploaded file. Drawing takes some 20 ms (the first one up to about 250 ms, reading the icons), so postResult starts
// it in its own goroutine: Finish never waits for it. A host that sent no seatStats has no rows: its post is the text
// style's whatever the setting.

// ResultImageName is the uploaded picture's file name (the embed shows it as attachment://scoreboard.png).
const ResultImageName = "scoreboard.png"

// ResultImage is what the picture shows: the rows (resultRows: teams 1 then 2, each in seat order) and the winning
// team (1 or 2; 0 = no winner).
type ResultImage struct {
	Winner int
	Rows   []ResultRow
}

// postImage draws the picture and posts the image style; without a renderer or when drawing fails it posts the text
// style (text) instead, with one log line either way. Runs in its own goroutine (postResult).
func (hg *hostedGame) postImage(rp *ResultPosts, r matchResult, text discordhook.Message, players int, bots [2]int) {
	winner := 0
	if r.WinnerTeam == 1 || r.WinnerTeam == 2 {
		winner = r.WinnerTeam
	}
	png, err := drawResult(rp.Image, ResultImage{Winner: winner, Rows: r.Rows})
	if err != nil {
		rp.Hook.Post(text)
		hg.s.Log.Logf("blaze", "   game %d: result posted to Discord (%d players, %d+%d bots) as text: no scoreboard picture (%v)",
			hg.game.ID, players, bots[0], bots[1], err)
		return
	}
	rp.Hook.Post(imageMessage(text, png))
	hg.s.Log.Logf("blaze", "   game %d: result posted to Discord (%d players, %d+%d bots) as a picture (%d KB)",
		hg.game.ID, players, bots[0], bots[1], (len(png)+1023)/1024)
}

// drawResult calls the renderer (nil: none; a panic in it is an error, not the server's end).
func drawResult(draw func(ResultImage) ([]byte, error), img ResultImage) (png []byte, err error) {
	if draw == nil {
		return nil, errors.New("no renderer: the art beside the content file is missing")
	}
	defer func() {
		if p := recover(); p != nil {
			png, err = nil, fmt.Errorf("the renderer failed: %v", p)
		}
	}()
	png, err = draw(img)
	if err == nil && len(png) == 0 {
		err = errors.New("the renderer drew nothing")
	}
	return png, err
}

// imageMessage is the image style's post: the text style's embed without its fields, its image the uploaded
// picture.
func imageMessage(text discordhook.Message, png []byte) discordhook.Message {
	m := discordhook.Message{Content: text.Content, PingRoles: text.PingRoles}
	if len(text.Embeds) > 0 { // the result post has one embed
		e := text.Embeds[0]
		e.Fields = nil
		e.Image = &discordhook.Image{URL: discordhook.AttachmentURL(ResultImageName)}
		m.Embeds = []discordhook.Embed{e}
	}
	m.Files = []discordhook.File{{Name: ResultImageName, ContentType: "image/png", Data: png}}
	return m
}
