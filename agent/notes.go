package agent

import agentcontext "Q-Mi/agent/context"

type Note = agentcontext.Note
type agentState = agentcontext.State

const maxNotes = 50

func LoadNotes(path string) ([]Note, error) { return agentcontext.LoadNotes(path) }
func AppendNote(path, text string) error    { return agentcontext.AppendNote(path, text) }
func FormatNotes(notes []Note) string       { return agentcontext.FormatNotes(notes) }

func readStateFrom(path string) (agentState, error) { return agentcontext.ReadState(path) }
func writeState(path, mood string) error            { return agentcontext.WriteState(path, mood) }
