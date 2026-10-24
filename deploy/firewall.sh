#!/bin/sh
# Pare-feu du serveur du site public (Debian ou Ubuntu, ufw). À lancer en root, une seule fois.
# Ouverts : SSH (22), HTTP (80, pour le certificat et la redirection), HTTPS (443, TCP et UDP pour
# HTTP/3), P2P du nœud (3000). Tout le reste est fermé en entrée. Le site du nœud (3001) n'écoute que
# sur 127.0.0.1 et n'a pas besoin de règle.
#
# Journal du pare-feu désactivé : il écrirait l'adresse IP de chaque connexion refusée.
set -eu

ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp comment "SSH"
ufw allow 80/tcp comment "HTTP (certificat, redirection)"
ufw allow 443/tcp comment "HTTPS"
ufw allow 443/udp comment "HTTPS (HTTP/3)"
ufw allow 3000/tcp comment "Etalon P2P"
ufw logging off
ufw --force enable
ufw status verbose
