package command

import (
	"time"

	"github.com/zitadel/zitadel/internal/domain"
	"github.com/zitadel/zitadel/internal/eventstore"
	"github.com/zitadel/zitadel/internal/repository/session"
)

type WebAuthNChallengeModel struct {
	Challenge          string
	AllowedCrentialIDs [][]byte
	UserVerification   domain.UserVerificationRequirement
	RPID               string
}

func (p *WebAuthNChallengeModel) WebAuthNLogin(human *domain.Human, credentialAssertionData []byte) *domain.WebAuthNLogin {
	return &domain.WebAuthNLogin{
		ObjectRoot:              human.ObjectRoot,
		CredentialAssertionData: credentialAssertionData,
		Challenge:               p.Challenge,
		AllowedCredentialIDs:    p.AllowedCrentialIDs,
		UserVerification:        p.UserVerification,
		RPID:                    p.RPID,
	}
}

type SessionWriteModel struct {
	eventstore.WriteModel

	TokenID              string
	UserID               string
	UserCheckedAt        time.Time
	PasswordCheckedAt    time.Time
	IntentCheckedAt      time.Time
	WebAuthNCheckedAt    time.Time
	TOTPCheckedAt        time.Time
	WebAuthNUserVerified bool
	Metadata             map[string][]byte
	State                domain.SessionState

	WebAuthNChallenge *WebAuthNChallengeModel

	aggregate *eventstore.Aggregate
}

func NewSessionWriteModel(sessionID string, resourceOwner string) *SessionWriteModel {
	return &SessionWriteModel{
		WriteModel: eventstore.WriteModel{
			AggregateID:   sessionID,
			ResourceOwner: resourceOwner,
		},
		Metadata:  make(map[string][]byte),
		aggregate: &session.NewAggregate(sessionID, resourceOwner).Aggregate,
	}
}

func (wm *SessionWriteModel) Reduce() error {
	for _, event := range wm.Events {
		switch e := event.(type) {
		case *session.AddedEvent:
			wm.reduceAdded(e)
		case *session.UserCheckedEvent:
			wm.reduceUserChecked(e)
		case *session.PasswordCheckedEvent:
			wm.reducePasswordChecked(e)
		case *session.IntentCheckedEvent:
			wm.reduceIntentChecked(e)
		case *session.WebAuthNChallengedEvent:
			wm.reduceWebAuthNChallenged(e)
		case *session.WebAuthNCheckedEvent:
			wm.reduceWebAuthNChecked(e)
		case *session.TOTPCheckedEvent:
			wm.reduceTOTPChecked(e)
		case *session.TokenSetEvent:
			wm.reduceTokenSet(e)
		case *session.TerminateEvent:
			wm.reduceTerminate()
		}
	}
	return wm.WriteModel.Reduce()
}

func (wm *SessionWriteModel) Query() *eventstore.SearchQueryBuilder {
	query := eventstore.NewSearchQueryBuilder(eventstore.ColumnsEvent).
		AddQuery().
		AggregateTypes(session.AggregateType).
		AggregateIDs(wm.AggregateID).
		EventTypes(
			session.AddedType,
			session.UserCheckedType,
			session.PasswordCheckedType,
			session.IntentCheckedType,
			session.WebAuthNChallengedType,
			session.WebAuthNCheckedType,
			session.TOTPCheckedType,
			session.TokenSetType,
			session.MetadataSetType,
			session.TerminateType,
		).
		Builder()

	if wm.ResourceOwner != "" {
		query.ResourceOwner(wm.ResourceOwner)
	}
	return query
}

func (wm *SessionWriteModel) reduceAdded(e *session.AddedEvent) {
	wm.State = domain.SessionStateActive
}

func (wm *SessionWriteModel) reduceUserChecked(e *session.UserCheckedEvent) {
	wm.UserID = e.UserID
	wm.UserCheckedAt = e.CheckedAt
}

func (wm *SessionWriteModel) reducePasswordChecked(e *session.PasswordCheckedEvent) {
	wm.PasswordCheckedAt = e.CheckedAt
}

func (wm *SessionWriteModel) reduceIntentChecked(e *session.IntentCheckedEvent) {
	wm.IntentCheckedAt = e.CheckedAt
}

func (wm *SessionWriteModel) reduceWebAuthNChallenged(e *session.WebAuthNChallengedEvent) {
	wm.WebAuthNChallenge = &WebAuthNChallengeModel{
		Challenge:          e.Challenge,
		AllowedCrentialIDs: e.AllowedCrentialIDs,
		UserVerification:   e.UserVerification,
		RPID:               e.RPID,
	}
}

func (wm *SessionWriteModel) reduceWebAuthNChecked(e *session.WebAuthNCheckedEvent) {
	wm.WebAuthNChallenge = nil
	wm.WebAuthNCheckedAt = e.CheckedAt
	wm.WebAuthNUserVerified = e.UserVerified
}

func (wm *SessionWriteModel) reduceTOTPChecked(e *session.TOTPCheckedEvent) {
	wm.TOTPCheckedAt = e.CheckedAt
}

func (wm *SessionWriteModel) reduceTokenSet(e *session.TokenSetEvent) {
	wm.TokenID = e.TokenID
}

func (wm *SessionWriteModel) reduceTerminate() {
	wm.State = domain.SessionStateTerminated
}

// AuthenticationTime returns the time the user authenticated using the latest time of all checks
func (wm *SessionWriteModel) AuthenticationTime() time.Time {
	var authTime time.Time
	for _, check := range []time.Time{
		wm.PasswordCheckedAt,
		wm.WebAuthNCheckedAt,
		wm.TOTPCheckedAt,
		wm.IntentCheckedAt,
		// TODO: add OTP (sms and email) check https://github.com/zitadel/zitadel/issues/6224
	} {
		if check.After(authTime) {
			authTime = check
		}
	}
	return authTime
}

// AuthMethodTypes returns a list of UserAuthMethodTypes based on succeeded checks
func (wm *SessionWriteModel) AuthMethodTypes() []domain.UserAuthMethodType {
	types := make([]domain.UserAuthMethodType, 0, domain.UserAuthMethodTypeIDP)
	if !wm.PasswordCheckedAt.IsZero() {
		types = append(types, domain.UserAuthMethodTypePassword)
	}
	if !wm.WebAuthNCheckedAt.IsZero() {
		if wm.WebAuthNUserVerified {
			types = append(types, domain.UserAuthMethodTypePasswordless)
		} else {
			types = append(types, domain.UserAuthMethodTypeU2F)
		}
	}
	if !wm.IntentCheckedAt.IsZero() {
		types = append(types, domain.UserAuthMethodTypeIDP)
	}
	if !wm.TOTPCheckedAt.IsZero() {
		types = append(types, domain.UserAuthMethodTypeTOTP)
	}
	// TODO: add checks with https://github.com/zitadel/zitadel/issues/6224
	/*
		if !wm.TOTPFactor.OTPSMSCheckedAt.IsZero() {
			types = append(types, domain.UserAuthMethodTypeOTPSMS)
		}
		if !wm.TOTPFactor.OTPEmailCheckedAt.IsZero() {
			types = append(types, domain.UserAuthMethodTypeOTPEmail)
		}
	*/
	return types
}
