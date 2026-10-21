now_ms() {
  n=$(date +%s%N 2>/dev/null)
  case "$n" in
    ''|*N)
      if [ -n "${EPOCHREALTIME:-}" ]; then
        e=${EPOCHREALTIME/./}
        echo $((e / 1000))
      else
        echo $(( $(date +%s) * 1000 ))
      fi
      ;;
    *) echo $(( n / 1000000 ));;
  esac
}
s=$(now_ms); end=$(( $(date +%s) + 30 ))
while :; do
  b=$(timeout 3 bash -c 'exec 3<>/dev/tcp/127.0.0.1/61000; head -c 4 <&3' 2>/dev/null)
  [ -n "$b" ] && break
  [ $(date +%s) -ge $end ] && { echo FAIL; exit 0; }
  sleep 0.5
done
c=$(( $(now_ms) - s )); out="OK $c"
for i in $(seq 1 5); do
  t0=$(now_ms); b=$(timeout 3 bash -c 'exec 3<>/dev/tcp/127.0.0.1/61000; head -c 4 <&3' 2>/dev/null); t1=$(now_ms)
  [ -n "$b" ] && out="$out $(( t1 - t0 ))"
done
echo "$out"
