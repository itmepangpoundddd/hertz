@echo off
setlocal
netsh advfirewall firewall delete rule name="HERTZ RTP Ingest (UDP 5004)" >nul 2>&1
netsh advfirewall firewall delete rule name="HERTZ ICE (UDP 8443)" >nul 2>&1
netsh advfirewall firewall delete rule name="HERTZ HTTP Listener (TCP 8080)" >nul 2>&1
exit /b 0
