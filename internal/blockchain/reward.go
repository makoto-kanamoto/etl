package blockchain

// RewardShare renvoie la part de chaque participant pour un bloc à n participants.
//
// Émission constante : DailyReward par bloc, sans halving — la stabilité vient de l'IPC, pas de la rareté.
// La récompense est partagée à parts égales entre les n nœuds ayant soumis une PoW valide.
// Chaque part est arrondie à l'unité inférieure ; le reste (moins de n unités) n'est pas émis.
// Le genesis (hauteur 0) ne rapporte rien : zéro allocation initiale (règle immuable n°3).
func RewardShare(height uint64, n int, p Params) uint64 {
	if height == 0 || n <= 0 {
		return 0
	}
	return p.DailyReward / uint64(n)
}
