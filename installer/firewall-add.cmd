@echo off
setlocal
rem Default HERTZ ports. If the operator changes ports in Settings, update the
rem Windows Firewall rules to match.
netsh advfirewall firewall delete rule name="HERTZ RTP Ingest (UDP 5004)" >nul 2>&1
netsh advfirewall firewall delete rule name="HERTZ ICE (UDP 8443)" >nul 2>&1
netsh advfirewall firewall delete rule name="HERTZ HTTP Listener (TCP 8080)" >nul 2>&1
netsh advfirewall firewall add rule name="HERTZ RTP Ingest (UDP 5004)" dir=in action=allow protocol=UDP localport=5004 profile=domain,private
netsh advfirewall firewall add rule name="HERTZ ICE (UDP 8443)" dir=in action=allow protocol=UDP localport=8443 profile=domain,private
netsh advfirewall firewall add rule name="HERTZ HTTP Listener (TCP 8080)" dir=in action=allow protocol=TCP localport=8080 profile=domain,private
exit /b 0
