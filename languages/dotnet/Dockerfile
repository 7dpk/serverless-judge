ARG JUDGE_BIN
FROM ${JUDGE_BIN} AS judge

FROM mcr.microsoft.com/dotnet/sdk:10.0
# Call the Roslyn compiler directly instead of `dotnet build`: no MSBuild,
# no project file, no restore. The reference list and runtime config that a
# project would generate are written once here.
RUN set -e; \
    csc=$(ls /usr/share/dotnet/sdk/*/Roslyn/bincore/csc.dll | head -1); \
    ref=$(ls -d /usr/share/dotnet/packs/Microsoft.NETCore.App.Ref/*/ref/net*/ | head -1); \
    rt=$(ls /usr/share/dotnet/shared/Microsoft.NETCore.App/ | sort -V | tail -1); \
    tfm=$(basename "$ref"); \
    test -f "$csc" && test -d "$ref" && test -n "$rt"; \
    mkdir -p /opt/lang; \
    { echo "-nologo"; echo "-optimize+"; echo "-langversion:latest"; echo "-target:exe"; \
      for f in "$ref"*.dll; do echo "-r:$f"; done; } > /opt/lang/csc.rsp; \
    printf '{"runtimeOptions":{"tfm":"%s","framework":{"name":"Microsoft.NETCore.App","version":"%s"},"configProperties":{"System.GC.Server":false,"System.GC.Concurrent":false}}}\n' "$tfm" "$rt" > /opt/lang/main.runtimeconfig.json; \
    printf '#!/bin/sh\ncp /opt/lang/main.runtimeconfig.json main.runtimeconfig.json\nexec dotnet %s @/opt/lang/csc.rsp "$@" -out:main.dll main.cs\n' "$csc" > /opt/lang/compile.sh; \
    chmod +x /opt/lang/compile.sh
ENV DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 DOTNET_EnableDiagnostics=0 DOTNET_gcServer=0

COPY --from=judge /judge /jexec /opt/judge/
COPY langs/ /opt/judge/langs/
ENTRYPOINT ["/opt/judge/judge", "serve"]
