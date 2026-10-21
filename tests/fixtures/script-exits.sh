echo "@@NAT"
{ iptables-save -t nat 2>/dev/null; ip6tables-save -t nat 2>/dev/null; } | grep -F -- '-j DNAT' | head -n 400
command -v nft >/dev/null 2>&1 && nft list ruleset 2>/dev/null | grep -F 'dnat' | head -n 400
# local and peer address of each ss line: the two fields before "users:(...)"
cols() { awk '{for(i=1;i<=NF;i++) if ($i ~ /^users:/) {print $(i-2), $(i-1); break}}'; }
for p in 443 8443; do
  echo "@@PORT $p"
  pid=$(ss -ltnpH "sport = :$p" 2>/dev/null | grep -oE 'pid=[0-9]+' | head -n1 | cut -d= -f2)
  [ -n "$pid" ] || continue
  c=$(cat /proc/$pid/comm 2>/dev/null)
  echo "@@PID $pid ${c:--}"
  echo "@@CMD $(tr '\0' ' ' < /proc/$pid/cmdline 2>/dev/null | head -c 600)"
  lp=$(ss -ltnpH 2>/dev/null | grep -F "((\"$c\"," | cols | awk '{n=split($1,a,":"); print a[n]}' | sort -u | tr '\n' ' ')
  ip=0
  # a bull-Tunnel engine here: the next kharej dials in on its tunnel port
  [ "$c" = bull-tunnel-engine ] && lp="$p" && ip=1
  echo "@@OUT"
  ss -tnpH state established 2>/dev/null | grep -F "((\"$c\"," | cols \
    | awk -v lp=" $lp " -v ip=$ip '{n=split($1,a,":"); if (index(lp, " " a[n] " ")) next; if (ip) sub(/:[0-9]+$/, "", $2); print $2}' \
    | grep -vE '^(127\.|\[::1\]|\[::ffff:127\.)' | sort | uniq -c | sort -rn | awk 'NR<=5 {print} END {print "@@DISTINCT " NR}'
done
echo "@@REACH"
for t in 1.2.3.4:443 '[2001:db8::1]:443'; do
  h=${t%:*}; h=${h#[}; h=${h%]}; q=${t##*:}
  if timeout 3 bash -c "</dev/tcp/$h/$q" 2>/dev/null; then echo "$t ok"; else echo "$t fail"; fi
done
echo "@@END"
