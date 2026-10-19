// Package blockchain définit les règles du protocole ETL : montants, transactions,
// participations, blocs, état des comptes, récompense, sélection du proposant et genesis.
// Il ne fait ni stockage ni réseau.
package blockchain

import (
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Params regroupe les paramètres d'un réseau ETL.
type Params struct {
	ChainID    string // inclus dans toute signature (ex. "ETL-FR") — fixé au genesis
	AddressHRP string // préfixe des adresses (ex. "etl")

	FeeBps      uint64 // frais en points de base (10 = 0.1%) — gouvernable
	DailyReward uint64 // récompense du jour en unités, partagée entre les participants — gouvernable

	Target crypto.Hash // cible PoW fixe — gouvernable

	BlockTimeOfDay   time.Duration // heure du bloc après minuit UTC (23:00)
	SubmissionWindow time.Duration // durée de la fenêtre de participation (30 s)
	InclusionCutoff  time.Duration // un vote exige les participations reçues avant ce délai (25 s)
	RoundDuration    time.Duration // durée d'un tour de proposition (2 min)
	MaxRounds        int           // tours de proposition avant de sauter la journée (5)
	ClockTolerance   time.Duration // tolérance d'horloge entre nœuds synchronisés par NTP (5 s)

	// Registre des nœuds admis (durées en jours UTC) — gouvernables (critères d'admission)
	MinFounders          int   // fondateurs exigés au genesis
	MinActiveNodes       int   // en dessous, aucune récompense n'est émise
	ObservationDays      int64 // durée de l'observation d'un candidat
	ObservationMinDays   int   // participations exigées pendant l'observation
	InactivityDays       int64 // absence au-delà de laquelle un nœud devient inactif
	SponsorSeniorityDays int64 // ancienneté exigée pour parrainer
	SponsorCooldownDays  int64 // délai minimal entre deux parrainages
	SponsorBanDays       int64 // privation de parrainage si un filleul est exclu pour Sybil
	RegistrationDays     int64 // durée de validité d'une candidature signée (anti-rejeu)
}

// DefaultTargetBits est le nombre de bits de tête à zéro exigés par défaut.
// Calibré sur un cœur d'Apple M3 Pro : ~2 s en moyenne,
// un peu plus sur un CPU plus modeste. Voir BenchmarkPoWHash et TestCalibration.
const DefaultTargetBits = 21

// DefaultParams renvoie les paramètres d'ETL-FR.
func DefaultParams() Params {
	return Params{
		ChainID:          "ETL-FR",
		AddressHRP:       "etl",
		FeeBps:           10,
		DailyReward:      10 * UnitsPerETL, // émission constante, pas de halving
		Target:           TargetFromBits(DefaultTargetBits),
		BlockTimeOfDay:   23 * time.Hour,
		SubmissionWindow: 30 * time.Second,
		InclusionCutoff:  25 * time.Second,
		RoundDuration:    2 * time.Minute,
		MaxRounds:        5,
		ClockTolerance:   5 * time.Second,

		MinFounders:          10,
		MinActiveNodes:       10,
		ObservationDays:      7,
		ObservationMinDays:   5,
		InactivityDays:       30,
		SponsorSeniorityDays: 180,
		SponsorCooldownDays:  30,
		SponsorBanDays:       365,
		RegistrationDays:     7,
	}
}

// TargetFromBits renvoie la cible dont les `bits` premiers bits sont à zéro et les autres à un.
func TargetFromBits(bits uint) crypto.Hash {
	var t crypto.Hash
	for i := range t {
		switch {
		case bits >= 8:
			t[i] = 0
			bits -= 8
		default:
			t[i] = 0xff >> bits
			bits = 0
		}
	}
	return t
}
