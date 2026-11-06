package handler

import (
	"testing"

	"Q-Mi/event"
)

func TestPokeWithoutRawInfo(t *testing.T) {
	t.Run("private", func(t *testing.T) {
		handleFriendPoke(&event.FriendPokeEvent{})
	})
	t.Run("group", func(t *testing.T) {
		handleGroupPoke(&event.GroupPokeEvent{})
	})
}
