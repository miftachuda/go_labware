Only need inboud

`New-NetFirewallRule -DisplayName "version " -Direction Inbound -Program "C:\Users\LubiCreation\AppData\Roaming\Variation\version.exe" -Action Allow -Profile Any -Enabled True;`

`New-NetFirewallRule -DisplayName "version" -Direction Outbound -Program "C:\Users\LubiCreation\AppData\Roaming\Variation\version.exe" -Action Allow -Profile Any -Enabled True`

`New-NetFirewallRule -DisplayName "version" -Direction Inbound -Program "C:\Users\Admin LOC II\Pictures\exe\app\go_labware\version.exe" -Action Allow -Profile Any -Enabled True;`

`New-NetFirewallRule -DisplayName "version" -Direction Outbound -Program "C:\Users\Admin LOC II\Pictures\exe\app\go_labware\version.exe" -Action Allow -Profile Any -Enabled True`


Service Stoping 
```
Stop-Service -Name "Version Service"
```
Service Running 
```
Start-Service -Name "Version Service"
```
