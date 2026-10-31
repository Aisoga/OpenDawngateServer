package mapedit

// The props' side (MAPED-EDIT3): the palette's nouns with their top-down icons, the nouns' models for the 3-D view,
// and, with every check of a map that has props, what its props place (`mapc props`). Everything comes from mapc's
// commands on the game copy; the icons and models land in the cache (game content, never in git).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// propsState caches the palette (the nouns and their icons, made once per editor run) and the nouns' model exports.
type propsState struct {
	mu     sync.Mutex
	nouns  []byte            // the /api/nouns reply
	models map[string]string // noun key -> the model's /3d/ URL ("" when it has none)
}

func (s *Server) routesProps() {
	s.mux.HandleFunc("GET /api/nouns", s.nounsList)
	s.mux.HandleFunc("POST /api/models", s.nounModels)
}

// nounKey is a noun's name without ".Noun", lower case.
func nounKey(n string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(n), ".Noun"))
}

// slashPath is a game path for comparing: lower case, forward slashes.
func slashPath(p string) string { return strings.ToLower(strings.ReplaceAll(p, `\`, "/")) }

type paletteNoun struct {
	Name          string          `json:"name"`
	Type          string          `json:"type"`
	Model         string          `json:"model,omitempty"`
	GraphicsScale float64         `json:"graphicsScale"`
	Box           json.RawMessage `json:"box"`
	Icon          *paletteIcon    `json:"icon,omitempty"`
}

type paletteIcon struct {
	URL      string     `json:"url"`
	Size     int        `json:"size"`
	Centre   [2]float64 `json:"centre"`
	PerPixel float64    `json:"metresPerPixel"`
}

// nounsList replies with the copy's dressing nouns (`mapc nouns`) and their icons (`mapc icon` into <cache>\icons,
// baked at the first request, about 4 s for build 854's 267 models, and kept): {"ok", "nouns": [...]}. A noun whose
// model the copy lacks has no icon (and draws nothing).
func (s *Server) nounsList(w http.ResponseWriter, r *http.Request) {
	if why := s.view3DReason(); why != "" {
		fail(w, http.StatusConflict, "%s", strings.Replace(why, "the 3-D view draws", "the props palette shows", 1))
		return
	}
	s.props.mu.Lock()
	defer s.props.mu.Unlock()
	if s.props.nouns != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(s.props.nouns)
		return
	}
	out, code, err := s.mapc(time.Minute, "nouns", "--json", s.c.Copy)
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	var res struct {
		OK    bool          `json:"ok"`
		Nouns []paletteNoun `json:"nouns"`
	}
	if json.Unmarshal(out, &res) != nil || !res.OK || code != 0 {
		reply(w, http.StatusOK, map[string]any{"ok": false, "exit": code, "result": json.RawMessage(orNull(out))})
		return
	}
	var withModel []string
	for _, n := range res.Nouns {
		if n.Model != "" {
			withModel = append(withModel, n.Name)
		}
	}
	icons := map[string]*paletteIcon{}
	failed := 0
	if len(withModel) > 0 {
		dir := filepath.Join(s.c.Cache, "icons")
		args := append(append([]string{"icon", "--json", s.c.Copy}, withModel...), dir)
		out, _, err := s.mapc(10*time.Minute, args...)
		if err != nil {
			fail(w, http.StatusInternalServerError, "%v", err)
			return
		}
		var ic struct {
			Icons []struct {
				Noun     string     `json:"noun"`
				File     string     `json:"file"`
				Size     int        `json:"size"`
				Centre   [2]float64 `json:"centre"`
				PerPixel float64    `json:"metresPerPixel"`
			} `json:"icons"`
			Failed int `json:"failed"`
		}
		if err := json.Unmarshal(out, &ic); err != nil {
			fail(w, http.StatusInternalServerError, "mapc icon's reply: %v", err)
			return
		}
		for _, i := range ic.Icons {
			icons[nounKey(i.Noun)] = &paletteIcon{URL: "/3d/icons/" + i.File, Size: i.Size, Centre: i.Centre,
				PerPixel: i.PerPixel}
		}
		failed = ic.Failed
	}
	for i := range res.Nouns {
		res.Nouns[i].Icon = icons[nounKey(res.Nouns[i].Name)]
	}
	b, _ := json.Marshal(map[string]any{"ok": true, "nouns": res.Nouns, "icons": len(icons), "noModel": failed})
	s.props.nouns = b
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// nounModels exports the named nouns' models for the 3-D view (`mapc model` into the cache, where the scenes' models
// are too): body {"nouns": [...]}, reply {"ok", "models": {"<noun key>": "/3d/<file>.gltf" or ""}}.
func (s *Server) nounModels(w http.ResponseWriter, r *http.Request) {
	if why := s.view3DReason(); why != "" {
		fail(w, http.StatusConflict, "%s", why)
		return
	}
	var q struct {
		Nouns []string `json:"nouns"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&q); err != nil || len(q.Nouns) > 2000 {
		fail(w, http.StatusBadRequest, "a body {\"nouns\": [...]} of at most 2000 names")
		return
	}
	s.props.mu.Lock()
	defer s.props.mu.Unlock()
	if s.props.models == nil {
		s.props.models = map[string]string{}
	}
	var want []string
	seen := map[string]bool{}
	for _, n := range q.Nouns {
		k := nounKey(n)
		if _, ok := s.props.models[k]; ok || seen[k] || k == "" || strings.ContainsAny(k, `/\:*?"<>|`) {
			continue
		}
		seen[k] = true
		want = append(want, k+".Noun")
	}
	if len(want) > 0 {
		// the nouns' model paths (mapc nouns), to tell which export is whose
		paths := map[string]string{}
		out, _, err := s.mapc(time.Minute, "nouns", "--json", s.c.Copy)
		if err != nil {
			fail(w, http.StatusInternalServerError, "%v", err)
			return
		}
		var ns struct {
			Nouns []paletteNoun `json:"nouns"`
		}
		json.Unmarshal(out, &ns)
		for _, n := range ns.Nouns {
			paths[nounKey(n.Name)] = slashPath(n.Model)
		}
		var known []string
		for _, n := range want {
			if paths[nounKey(n)] != "" {
				known = append(known, n)
			} else {
				s.props.models[nounKey(n)] = ""
			}
		}
		if len(known) > 0 {
			out, _, err = s.mapc(10*time.Minute, append(append([]string{"model", "--json", s.c.Copy}, known...),
				s.c.Cache)...)
			if err != nil {
				fail(w, http.StatusInternalServerError, "%v", err)
				return
			}
			var ms struct {
				Models []struct {
					Source string `json:"source"`
					File   string `json:"file"`
				} `json:"models"`
			}
			json.Unmarshal(out, &ms)
			byPath := map[string]string{}
			for _, m := range ms.Models {
				byPath[slashPath(m.Source)] = "/3d/" + filepath.ToSlash(m.File)
			}
			for _, n := range known {
				s.props.models[nounKey(n)] = byPath[paths[nounKey(n)]]
			}
		}
	}
	res := map[string]string{}
	for _, n := range q.Nouns {
		res[nounKey(n)] = s.props.models[nounKey(n)]
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "models": res})
}

// hasProps reports whether a map.json text places props (props[] or scatter[] not empty).
func hasProps(text []byte) bool {
	var m struct {
		Props   []json.RawMessage `json:"props"`
		Scatter []json.RawMessage `json:"scatter"`
	}
	return json.Unmarshal(text, &m) == nil && (len(m.Props) > 0 || len(m.Scatter) > 0)
}

// placed runs `mapc props` on a draft: every marker its props place (both teams, the map.json entry each comes
// from) and the placing's findings, or nil when it fails (a structural rule: the check's report says why).
func (s *Server) placed(draft string) (json.RawMessage, []map[string]any) {
	out, _, err := s.mapc(2*time.Minute, "props", "--json", "--maps", s.c.Maps, draft, s.c.Copy)
	if err != nil {
		return nil, nil
	}
	var res struct {
		Props    json.RawMessage  `json:"props"`
		Findings []map[string]any `json:"findings"`
	}
	if json.Unmarshal(out, &res) != nil || res.Props == nil {
		return nil, nil
	}
	return res.Props, res.Findings
}

// mergeFindings adds the props' placing findings (props.type, props.group, props.shadow) to a check's report when it
// doesn't hold them yet; an added error fails the report.
func mergeFindings(report json.RawMessage, more []map[string]any) json.RawMessage {
	if len(more) == 0 || report == nil {
		return report
	}
	var r map[string]any
	if json.Unmarshal(report, &r) != nil {
		return report
	}
	fs, _ := r["findings"].([]any)
	have := map[string]bool{}
	for _, f := range fs {
		if m, ok := f.(map[string]any); ok {
			have[fmt.Sprint(m["rule"], "|", m["path"], "|", m["message"])] = true
		}
	}
	keys := make([]string, 0, len(more))
	byKey := map[string]map[string]any{}
	for _, f := range more {
		k := fmt.Sprint(f["rule"], "|", f["path"], "|", f["message"])
		if !have[k] {
			keys = append(keys, k)
			byKey[k] = f
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		f := byKey[k]
		fs = append(fs, f)
		if f["severity"] == "error" {
			r["ok"] = false
		}
	}
	r["findings"] = fs
	b, err := json.Marshal(r)
	if err != nil {
		return report
	}
	return b
}
