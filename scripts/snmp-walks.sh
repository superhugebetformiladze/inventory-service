#!/bin/sh
# Снять сырые обходы коммутаторов для службы — когда под рукой net-snmp, а не
# netscan.exe.
#
#   SNMP_COMMUNITY=... scripts/snmp-walks.sh switches.txt ./обходы
#
# Обходится ровно то, что читает сбор (internal/netscan, Roots). Вывод — в
# виде -On -Ox: числовые OID и строки октетов байтами; иначе MAC-адреса из
# печатных байтов приходят текстом и теряются. Файл называется адресом
# устройства — по имени служба узнаёт, кого опросили.
#
# Принять: make netscan-import DIR=./обходы
set -eu

list=${1:?список адресов}
out=${2:?каталог для обходов}
: "${SNMP_COMMUNITY:?задайте SNMP_COMMUNITY}"

mkdir -p "$out"

grep -v '^[[:space:]]*#' "$list" | sed 's/#.*//' | while read -r ip _; do
    [ -n "$ip" ] || continue
    file="$out/$ip.walk"
    : > "$file"
    ok=1
    for root in .1.3.6.1.2.1.1 .1.3.6.1.2.1.2.2.1 .1.3.6.1.2.1.31.1.1.1 \
                .1.3.6.1.2.1.17 .1.0.8802.1.1.2.1 .1.3.6.1.2.1.4.22.1; do
        snmpbulkwalk -v2c -c "$SNMP_COMMUNITY" -On -Ox -t 5 -r 1 "$ip" "$root" >> "$file" 2>/dev/null || ok=0
    done
    if [ "$ok" = 1 ]; then echo "✓ $ip"; else echo "✗ $ip — часть обходов не удалась, см. $file"; fi
done
