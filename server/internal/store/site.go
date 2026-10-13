package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// This file holds what the website's self-service needs from the database: a chosen display name's
// check and the deletion of an account by its player. No schema change: a deleted account keeps its row (and its
// match history) with the personal data taken out.

// DeletedName is the display name a deleted account's persona gets, and the name other accounts' lists show for it.
const DeletedName = "Deleted player"

// deletedKey is the login name (email) a deleted account keeps: '#' is no login-name character (accounts.CheckName),
// so it can't collide with a real one, and the account's old name is free again.
func deletedKey(prefix string, id int64) string {
	return "#deleted-" + prefix + strconv.FormatInt(id, 10)
}

// personaTaken reports whether a persona of any account has this display name (compared as PersonaByName compares).
func personaTaken(q querier, name string) (bool, error) {
	var one int
	err := q.QueryRow(`SELECT 1 FROM personas WHERE name_key = ? LIMIT 1`, nameKey(name)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// DisplayNameTaken reports whether a persona already has this display name.
func (s *Store) DisplayNameTaken(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return personaTaken(s.db, name)
}

// DeleteAccount is a player's deletion of the own account (the website), in one transaction:
//
//   - the personal data goes: the login name (the account keeps "#deleted-u<user id>", so the name is free again),
//     the password hash, the display names (DeletedName; their lookup keys "#deleted-p<persona id>", and PersonaByName
//     never finds a deleted account), the client's settings, the user-info bits, the friends, recent-players and
//     blocked lists, the inventory and the perk pages;
//   - the status becomes StatusDeleted (a login of the old name is then an unknown name), the role RolePlayer;
//   - the match history stays without the name: the account's row with its level and XP, its stats and its history
//     by shaper, role and day (the profile's panels), keyed by the anonymous user id;
//   - other accounts lose the persona from their friends and blocked lists, and their recent players show it as
//     DeletedName.
//
// ErrNoAccount if there is no such account.
func (s *Store) DeleteAccount(userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := deletedKey("u", userID)
	res, err := tx.Exec(`UPDATE accounts SET email = ?, email_key = ?, password = '', status = ?, user_info = 0, role = ?
		WHERE user_id = ?`, key, key, int(StatusDeleted), int(RolePlayer), userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrNoAccount
	}
	var personas []int64
	if err := each(tx, func(r *sql.Rows) error {
		var id int64
		if err := r.Scan(&id); err != nil {
			return err
		}
		personas = append(personas, id)
		return nil
	}, `SELECT persona_id FROM personas WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, id := range personas {
		if _, err := tx.Exec(`UPDATE personas SET name = ?, name_key = ? WHERE persona_id = ?`, DeletedName,
			deletedKey("p", id), id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM list_members WHERE persona_id = ? AND list IN (?, ?)`, id, listFriends,
			listBlocked); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE list_members SET name = ? WHERE persona_id = ?`, DeletedName, id); err != nil {
			return err
		}
	}
	for _, table := range []string{"settings", "list_members", "items", "perk_pages"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("%s: %v", table, err)
		}
	}
	return tx.Commit()
}
