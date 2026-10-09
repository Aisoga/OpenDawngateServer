package blaze

// LivingLore component (0x0826), Dawngate's own (B4): the community vote on the game's story.
const (
	ComponentLivingLore = 0x0826
	CmdLLGetCurrentVote = 0x6E
)

func registerLivingLore(s *Server) {
	s.Register(ComponentLivingLore, CmdLLGetCurrentVote, "getCurrentVote", func(r *Request) []byte {
		return GetCurrentVoteReply()
	})
}

// GetCurrentVoteReply is LivingLore::getCurrentVote (GetCurrentVoteRequest) ->
// Blaze::LivingLore::GetCurrentVoteResponse (member table 0x01461240: CUV enum +0x5C, CVT struct +0x08;
// RPC job 0x012B97F8 at 0x00AD941F). No vote running: both left at their defaults.
func GetCurrentVoteReply() []byte {
	return nil
}
