package dgserver

import (
	"dawngate/server/internal/blaze"
	"dawngate/server/internal/content"
	"dawngate/server/internal/discordhook"
	"dawngate/server/internal/scoreboard"
)

// resultImage is the result post's picture renderer for blaze.ResultPosts.Image (--discord-result-style image): the
// scoreboard drawn from the art beside the content file (internal/scoreboard: the fonts parsed here, once; the icons
// and portraits read when first drawn). It returns nil and the start-up note with the text style, or with the image
// style when the art isn't there (no content file, no art folder, no font: each result is then posted as text with
// one log line); else the renderer and its note.
func resultImage(s discordhook.Settings, contentFile string) (func(blaze.ResultImage) ([]byte, error), string) {
	if s.Style() != discordhook.StyleImage {
		return nil, ""
	}
	r, err := scoreboard.Load(contentFile)
	if err != nil {
		return nil, "discord result picture: none, results are posted as text: " + err.Error()
	}
	note := "discord result picture: drawn from the art beside " + contentFile + " (" + scoreboard.TextFont + ", " +
		scoreboard.HeadFont + ", " + "item icons from " + content.IconDir(contentFile) + ", portraits from " +
		content.PortraitDir(contentFile) + ")"
	return func(img blaze.ResultImage) ([]byte, error) { return r.PNG(scoreboardOf(img)) }, note
}

// scoreboardOf is a result's rows as the renderer takes them.
func scoreboardOf(img blaze.ResultImage) scoreboard.Board {
	b := scoreboard.Board{Winner: img.Winner, Rows: make([]scoreboard.Row, 0, len(img.Rows))}
	for _, r := range img.Rows {
		row := scoreboard.Row{Team: r.Team, Name: r.Name, Bot: r.Bot, Left: r.Left, Result: r.Result, Shaper: r.Shaper,
			Portrait: r.Portrait, Level: r.Level, Kills: r.Kills, Deaths: r.Deaths, Assists: r.Assists, CS: r.CS,
			Vim: r.Vim}
		for i := 0; i < min(blaze.ResultSlots, scoreboard.Slots); i++ {
			row.Items[i] = r.Items[i].File
		}
		b.Rows = append(b.Rows, row)
	}
	return b
}
