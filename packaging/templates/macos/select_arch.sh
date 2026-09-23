#!/bin/sh
# Source this file from a trusted bootstrap script, then call
# zatiti_native_mac_arch. It prints arm64 or amd64 and fails closed for
# unknown probe results. No artifact URL or command execution is accepted.

zatiti_select_mac_arch() {
    zatiti_arm_status=$1
    zatiti_arm_value=$2
    zatiti_machine=$3
    zatiti_arm_error=$4
    case "$zatiti_arm_status:$zatiti_arm_value" in
        0:1)
            # hw.optional.arm64 reports the hardware even under Rosetta;
            # uname may still say x86_64 in a translated shell.
            printf '%s\n' arm64
            return 0
            ;;
        0:0)
            if [ "$zatiti_machine" = x86_64 ]; then
                printf '%s\n' amd64
                return 0
            fi
            ;;
        *)
            # Intel Macs can lack this OID. Other sysctl failures must not
            # silently select Intel on an Apple Silicon machine.
            if [ "$zatiti_arm_error" = "sysctl: unknown oid 'hw.optional.arm64'" ] && [ "$zatiti_machine" = x86_64 ]; then
                printf '%s\n' amd64
                return 0
            fi
            ;;
    esac
    return 1
}

zatiti_native_mac_arch() {
    zatiti_probe=$(/usr/sbin/sysctl -n hw.optional.arm64 2>&1)
    zatiti_status=$?
    zatiti_machine=$(/usr/bin/uname -m) || return 1
    if [ "$zatiti_status" -eq 0 ]; then
        zatiti_select_mac_arch "$zatiti_status" "$zatiti_probe" "$zatiti_machine" ''
    else
        zatiti_select_mac_arch "$zatiti_status" '' "$zatiti_machine" "$zatiti_probe"
    fi
}
