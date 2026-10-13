package main

// Lista de respawns da guild (mesma lista das capturas enviadas).
// Serve só para sugerir códigos e mostrar o nome; o app aceita outros códigos também.

type Respawn struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Region string `json:"region"`
}

var catalogGroups = []struct {
	Region string
	Items  [][2]string
}{
	{"Ab'Dendriel", [][2]string{{"AB1", "Forgotten Crypt"}, {"AB2", "Forsaken Crypt"}, {"AB3", "Unhallowed Crypt"}}},
	{"Ankrahmun", [][2]string{{"A1", "Cobra Castelo"}, {"A2", "Cobra Subsolo"}, {"A3", "Oskayaat Undercity"}, {"A4", "Murky Caverns"}, {"A5", "Gold Token Darama"}}},
	{"Carlin", [][2]string{{"C1", "Livraria de Fire"}, {"C2", "Livraria de Gelo"}, {"C3", "Livraria de Energy"}}},
	{"Criatura Boostada", [][2]string{{"X2", "Boosted Creature"}}},
	{"Darashia", [][2]string{{"D1", "Plague Full"}, {"D2", "Plague -1"}, {"D3", "Plague -2"}, {"D4", "Jugger Full"}, {"D7", "DT -1"}, {"D8", "DT -2"}, {"D9", "Pumin -1, -2"}, {"D11", "Pumin Andar do Boss"}, {"D12", "Infinito"}, {"D13", "Undead Seal -1"}, {"D14", "HFF Seal"}, {"D15", "Caminho Ferumbras"}, {"D16", "Eskeleto Darashia"}, {"D17", "Isles"}, {"D18", "Lion Sanctum -1"}, {"D19", "Lion Sanctum -2"}, {"D20", "Burster Spectre-Azul"}, {"D21", "Bazir Seal"}, {"D22", "Undead Seal -2"}}},
	{"Edron", [][2]string{{"E1", "DT - Inquisition"}, {"E2", "Carnisylvans"}, {"E3", "Azzilon Castle"}, {"E4", "Azzilon -1"}, {"E5", "Azzilon -2, -3"}, {"E6", "Azzilon -4"}, {"E7", "Crypt Warrior"}}},
	{"Farmine", [][2]string{{"F1", "Falcon Subsolo"}, {"F2", "Falcon Castle"}, {"F4", "Gold Token Zao"}, {"F5", "Nimmersatt Dragons"}, {"F6", "Nimmersatt Dragons +1"}}},
	{"Feyrist", [][2]string{{"F10", "Elfo de Fogo"}, {"F11", "Elfo de Gelo"}}},
	{"Gnomprona", [][2]string{{"G1", "Monster Graveyard Esq"}, {"G2", "Crystal Enigma"}, {"G4", "Sparkling Pools"}, {"G5", "Sparkling Pools Alt"}, {"G6", "Monster Graveyard Direita"}}},
	{"Gray Island", [][2]string{{"GI1", "Deathling"}}},
	{"Issavi", [][2]string{{"I1", "Issavi Sewer"}, {"I2", "Labirinto"}, {"I3", "Goannas"}, {"I4", "Crypt Warden -2"}, {"I5", "Girtablilu"}, {"I6", "Bashmu"}}},
	{"Marapur", [][2]string{{"M1", "Naga"}, {"M2", "Great Pearl Fan Reef"}, {"M3", "Asura Citadel -1"}, {"M4", "Asura Citadel -2"}, {"M5", "Radiant Skyhold"}, {"M6", "Radiant Ascendancy"}}},
	{"Oramond", [][2]string{{"O1", "Rathleton Catacombs"}, {"O2", "Oramond Mountain Hideout"}, {"O4", "Podzilla-Quaras"}, {"O5", "Podzilla-Rootthing -2"}, {"O6", "Podzilla-Rootthing -1"}}},
	{"Port Hope", [][2]string{{"P1", "Asura Castle"}, {"P2", "Asura Espelho"}, {"P3", "True Asura -1"}, {"P4", "True Asura -2"}, {"P5", "Gazer Spectre-Vermelho"}, {"P6", "Iksupan Waterways"}, {"P7", "Lost Souls Netherworld"}, {"P8", "Iksupan Undercity"}}},
	{"Roshamuul", [][2]string{{"R1", "West"}, {"R2", "Bones"}, {"R3", "Shockhead"}, {"R4", "Prison -3"}, {"R5", "Ingol Surface"}, {"R6", "Deeper Ingol -1"}, {"R7", "Deeper Ingol -2"}, {"R8", "Deeper Ingol -3"}, {"R9", "Upper Roshamuul"}, {"R10", "Bloodfire Gorge"}, {"R11", "Stag Bastion"}}},
	{"Rotten Blood", [][2]string{{"B1", "Jaded Roots"}, {"B2", "Putrefactory"}, {"B3", "Gloom Pillars"}, {"B4", "Darklight Core"}}},
	{"Svargrond", [][2]string{{"S1", "Gold Token Svargrond"}}},
	{"Venore", [][2]string{{"V1", "Ripper Spectre-Verde"}, {"V3", "Lost Soul Brain Ground"}, {"V5", "Cathedral -3"}, {"V6", "Bulltaur Lair -1"}, {"V7", "Bulltaur Lair -2"}}},
	{"Warzone / Gnomegate", [][2]string{{"T1", "Warzone 1"}, {"T2", "Warzone 2"}, {"T3", "Warzone 3"}, {"T4", "Warzone 4"}, {"T5", "Warzone 5"}, {"T6", "Warzone 6"}, {"T7", "Warzone 7"}, {"T8", "Warzone 8"}, {"T9", "Warzone 9"}}},
	{"Yalahar", [][2]string{{"Y1", "Souls Fire Raid"}, {"Y2", "Souls Fear"}, {"Y4", "Souls Earth"}, {"Y5", "Souls Energy"}, {"Y6", "Souls Thais"}, {"Y7", "Souls Earth Alt"}, {"Y8", "Yalahar Cemetery"}, {"Y9", "Norcferatu Fortress"}, {"Y10", "Norcferatu Catacombs Esq"}, {"Y11", "Norcferatu Catacombs Dir"}}},
}

func Catalog() []Respawn {
	var out []Respawn
	for _, g := range catalogGroups {
		for _, it := range g.Items {
			out = append(out, Respawn{Code: it[0], Name: it[1], Region: g.Region})
		}
	}
	return out
}

func CatalogName(code string) string {
	for _, r := range Catalog() {
		if r.Code == code {
			return r.Name
		}
	}
	return ""
}
