# LinuxFr.org : dépêche

À proposer dans l'espace de rédaction (section « Administration système » ou
« Logiciels libres »), avec les liens dans le champ prévu.

**Titre :**

> Alder 1.42 : une interface web et une ligne de commande pour OpenLDAP et 389 Directory Server

**Liens :**

- Version 1.42.0 : https://github.com/Hazame-hub/alder/releases/tag/v1.42.0
- Démarrage rapide : https://github.com/Hazame-hub/alder/blob/main/docs/GETTING-STARTED.md
- Démonstration animée d'une modification : https://github.com/Hazame-hub/alder/blob/main/docs/screenshots/edit-review-apply.gif
- Code source (AGPL-3.0) : https://github.com/Hazame-hub/alder

**Chapeau :**

Alder est un outil libre, sous licence AGPL-3.0, pour administrer un annuaire
OpenLDAP ou 389 Directory Server. C'est un seul binaire écrit en Go, qui sert
une interface web et fait aussi office de client en ligne de commande. La
version 1.42.0 vient de sortir. Son principe : aucune écriture n'atteint
l'annuaire sans avoir été montrée d'abord sous forme de LDIF.

**Corps :**

## Pourquoi un outil de plus

Administrer un annuaire LDAP, c'est souvent `ldapsearch`, du LDIF écrit à la
main et une vieille console d'administration à qui l'on ne fait qu'à moitié
confiance. Alder part d'une idée simple : une modification d'annuaire est du
code, et doit pouvoir être relue comme du code.

## Ce que fait Alder

- **Chaque écriture est planifiée puis montrée.** Qu'il s'agisse d'un attribut
  ou d'un lot de quarante modifications, Alder lit d'abord les entrées
  concernées, indique ce que la modification fera réellement et affiche
  l'enregistrement de changement RFC 2849 exact qui sera envoyé. La même
  modification s'exporte en tâche Ansible (`community.general`).
- **Un conflit est refusé, pas écrasé.** Le plan porte une empreinte de l'état
  lu. Si quelqu'un a modifié l'entrée entre le plan et l'application, rien
  n'est écrit.
- **L'éditeur connaît le schéma.** Attributs obligatoires signalés,
  attributs opérationnels verrouillés, appartenance aux groupes imbriqués avec
  la chaîne qui y mène, références vers une entrée avant de la supprimer.
- **L'état voulu d'un annuaire peut vivre dans Git.** Un projet Alder, c'est
  un `alder.yaml` à côté de fichiers LDIF. `alder project plan
  --detailed-exitcode` indique à la CI si l'annuaire correspond encore au
  dépôt. Une entrée que les fichiers ne décrivent pas est signalée, jamais
  supprimée.
- **Deux serveurs, un même comportement.** OpenLDAP et 389 DS sont traités à
  égalité, et une même suite de conformité exécute chaque cas sur les deux.
- **Sobre à exploiter.** Pas de base de données, aucun état sur disque, les
  identifiants restent en mémoire le temps de la session. Alder refuse de
  démarrer sans TLS, sauf avec `--allow-http`, et `--read-only` interdit toute
  écriture.

## Nouveautés récentes

- Un **second serveur en lecture seule** dans la même session (une recette à
  côté de la production, un réplica à côté de son fournisseur), pour comparer
  données, schéma et configuration, ou une entrée sur les deux.
- Des **plans enregistrés** (`alder plan --out`, `alder apply --plan`, et leur
  équivalent pour les projets) qu'on peut **signer** avec une clé Ed25519 :
  le serveur lancé avec `--trusted-keys` n'applique que les plans signés par
  une clé de confiance, et exactement ce qu'ils contiennent.
- L'ouverture d'un **projet dans le navigateur**, depuis un dossier ou un
  `.zip`.
- Les **règles d'accès** de l'arbre de configuration (`olcAccess`, `aci`)
  sont désormais restaurables depuis un paquet de récupération.
- Des **recherches enregistrées** (`*.search.yaml`), la **modification d'un
  attribut sur plusieurs entrées** sélectionnées, et des outils pour **ajouter
  ou vérifier de nombreux membres** d'un groupe.
- Un plan enregistré peut exiger **plusieurs signataires** (`--min-signers`)
  et porter des **renommages et déplacements** d'entrées (1.40 et 1.42).
- L'**état de la réplication** comparé entre deux serveurs, origine par
  origine, en temps de retard, et un **conseiller d'index** qui lit un journal
  d'accès fourni par l'administrateur (1.41).

## Installation

Des archives pour Linux, macOS et Windows (amd64 et arm64), avec
`checksums.txt` et un SBOM, sont publiées sur la page de la version. L'image
`ghcr.io/hazame-hub/alder:1.42.0` existe pour amd64 et arm64. Le guide de
démarrage lance un OpenLDAP et un 389 DS d'essai avec Docker et accompagne une
première modification en une dizaine de minutes.

## Limites

Le projet est jeune : sa première version date de septembre 2026. Active
Directory, FreeIPA et Entra ID ne sont pas pris en charge. Les retours, en
particulier d'administrateurs OpenLDAP et 389 DS, sont les bienvenus.
