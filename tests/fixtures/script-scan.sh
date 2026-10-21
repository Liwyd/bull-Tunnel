for f in /etc/bull-tunnel/*.toml; do
  [ -f "$f" ] || continue
  n=$(basename "$f" .toml)
  u="bull-tunnel-$n.service"
  if ! systemctl cat "$u" >/dev/null 2>&1; then
    u=$(grep -lsF -- "$f" /etc/systemd/system/*.service /lib/systemd/system/*.service /usr/lib/systemd/system/*.service 2>/dev/null | head -n1)
    u=${u##*/}
  fi
  a=-
  [ -n "$u" ] && a=$(systemctl is-active "$u" 2>/dev/null)
  echo "@@FILE $n ${u:--} ${a:--}"
  head -c 65536 "$f"
  echo
done
echo "@@END"
