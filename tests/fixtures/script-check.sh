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

a=$(systemctl is-active bulltunnel-bt-l.service 2>/dev/null)
c=$(grep -o '"connected": *[a-z]*' /etc/bull-tunnel/bt-l.metrics.json 2>/dev/null | grep -o '[a-z]*$')
t0=$(now_ms); b=$(timeout 3 bash -c 'exec 3<>/dev/tcp/127.0.0.1/61000; head -c 4 <&3' 2>/dev/null); t1=$(now_ms); p=$( [ -n "$b" ] && echo 1 || echo 0)
e=$(cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk '{ split($2,a,":"); if (a[2]=="0C08" && $4=="01") n++ } END { print n+0 }')
echo "@@l|$a|$c|$p|$(( t1 - t0 ))|$e"
