# Deployment
```powershell
.\scripts\install-all.ps1 `
    -InstallDir "D:\Kerja\apps\silapet" `
-NssmPath "D:\Kerja\dev\nssm-2.24\nssm-2.24\win64"
```
## or separately

```powershell
.\scripts\install-service.ps1 `
    -InstallDir "D:\Kerja\apps\silapet" `
    -NssmPath "D:\Kerja\dev\nssm-2.24\nssm-2.24\win64"
```
```powershell
.\scripts\install-funnel-task.ps1
```