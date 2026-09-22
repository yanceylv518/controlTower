package archivepipeline

import "encoding/json"

// Decode fails closed on corrupt checkpoints. Never fall back to New on error.
func Decode(raw []byte) (State, error) {
	var s State
	if json.Unmarshal(raw, &s) != nil || s.Validate() != nil {
		return State{}, ErrConflict
	}
	return s, nil
}

func (s State) Encode() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

func (s State) Validate() error {
	if !s.Settings.Validate() || s.Version != 1 || s.Days == nil || s.Active == nil || len(s.Active) > 2 || (s.MigrationDone && !s.SchemaReady) {
		return ErrConflict
	}
	if (s.Round == 0 && s.Cutoff != "") || (s.Round != 0 && !validDate(s.Cutoff)) || (s.Frontier != "" && !validDate(s.Frontier)) {
		return ErrConflict
	}
	for date, d := range s.Days {
		if !validDate(date) || d == nil || d.Revision == 0 || d.OrganizedRevision > d.Revision || d.Collected != (date < s.Frontier) {
			return ErrConflict
		}
		for _, r := range []*Result{d.Result, d.LastSeal} {
			if r == nil {
				continue
			}
			if (r.Diagnostic != nil && r.Diagnostic.Validate() != nil) || (r.CompletedAt != nil && r.CompletedAt.IsZero()) || r.Revision == 0 || r.Revision > d.Revision || r.Round == 0 || r.Round > s.Round || len(r.Code) > 128 || len(r.SealVersion) > 128 || (r.Code == "") == (r.SealVersion == "") {
				return ErrConflict
			}
		}
		if d.Result != nil && d.Result.Revision != d.Revision {
			return ErrConflict
		}
		if d.LastSeal != nil && d.LastSeal.SealVersion == "" {
			return ErrConflict
		}
	}
	tokens := map[uint64]bool{}
	for task, w := range s.Active {
		if !s.SchemaReady || task != w.Task || w.Token == 0 || w.Token > s.NextToken || tokens[w.Token] {
			return ErrConflict
		}
		tokens[w.Token] = true
		switch task {
		case Migration:
			if s.MigrationDone || w.Date != "" || w.Revision != 0 {
				return ErrConflict
			}
		case Collection:
			if w.Date != "" || w.Revision != 0 {
				return ErrConflict
			}
		case Organization, Verification:
			d := s.Days[w.Date]
			if !s.MigrationDone || s.Round == 0 || w.Date > s.Cutoff || d == nil || !d.Collected || w.Revision == 0 || w.Revision > d.Revision {
				return ErrConflict
			}
		default:
			return ErrConflict
		}
	}
	if _, ok := s.Active[Verification]; ok && len(s.Active) != 1 {
		return ErrConflict
	}
	if _, ok := s.Active[Migration]; ok {
		if _, other := s.Active[Organization]; other {
			return ErrConflict
		}
	}
	return nil
}
