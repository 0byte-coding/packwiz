#!/bin/bash
# Script to test rate limit handling in packwiz
# This will attempt to install many mods quickly to potentially trigger rate limiting

set -e

echo "========================================="
echo "Packwiz Rate Limit Handling Test"
echo "========================================="
echo ""
echo "This script will:"
echo "1. Create a fresh test pack"
echo "2. Attempt to install multiple mods with dependencies rapidly"
echo "3. Monitor for rate limit messages"
echo ""

# Create a temporary directory for testing
TEST_DIR="/tmp/packwiz-ratelimit-test-$(date +%s)"
mkdir -p "$TEST_DIR"
cd "$TEST_DIR"

echo "Test directory: $TEST_DIR"
echo ""

# Initialize a test pack
echo "Initializing test pack..."
/tmp/packwiz init --name "Rate Limit Test" --mc-version "1.21.1" --modloader fabric -y > /dev/null 2>&1

# List of mods to install (many with dependencies)
MODS=(
    "fabric-api"
    "rei"  # Roughly Enough Items - has dependencies
    "modmenu"
    "sodium"
    "lithium"
    "xaeros-minimap"
    "entitytexturefeatures"
    "xaeros-world-map"
    "entity-model-features"
    "appleskin"
    "not-enough-animations"
    "iris"
    "cloth-config"
    "architectury-api"
    "terrablender"
    "geckolib"
)

echo "Attempting to install ${#MODS[@]} mods rapidly..."
echo "Watch for 'Rate limited by Modrinth API' messages below:"
echo "========================================="
echo ""

RATE_LIMITED=0
SUCCESS=0
FAILED=0

for mod in "${MODS[@]}"; do
    echo ">> Installing $mod..."
    if OUTPUT=$(/tmp/packwiz modrinth install "$mod" -y 2>&1); then
        echo "$OUTPUT"
        if echo "$OUTPUT" | grep -q "Rate limited by Modrinth API"; then
            echo "✓ RATE LIMIT DETECTED AND HANDLED for $mod!"
            RATE_LIMITED=$((RATE_LIMITED + 1))
        fi
        if echo "$OUTPUT" | grep -q "successfully added"; then
            SUCCESS=$((SUCCESS + 1))
        fi
    else
        echo "$OUTPUT"
        if echo "$OUTPUT" | grep -q "rate limit exceeded after"; then
            echo "✗ RATE LIMIT EXCEEDED (fix didn't work - needs investigation)"
        fi
        FAILED=$((FAILED + 1))
    fi
    echo ""
done

echo "========================================="
echo "Test Results:"
echo "========================================="
echo "Successfully installed: $SUCCESS mods"
echo "Failed installations: $FAILED mods"
echo "Rate limits encountered and handled: $RATE_LIMITED"
echo ""

if [ $RATE_LIMITED -gt 0 ]; then
    echo "✓ SUCCESS: Rate limiting was encountered and handled automatically!"
    echo "  The fix is working - packwiz retried and succeeded."
else
    echo "⚠ No rate limiting was encountered during this test."
    echo "  This could mean:"
    echo "  1. The Modrinth API isn't currently rate limiting (good!)"
    echo "  2. The request rate wasn't high enough to trigger limits"
    echo ""
    echo "  To manually trigger rate limiting, try:"
    echo "  - Running this script multiple times in quick succession"
    echo "  - Installing a mod with many nested dependencies"
    echo "  - Running during peak API usage times"
fi

echo ""
echo "Test directory preserved at: $TEST_DIR"
echo "Check the installed mods with: cd $TEST_DIR && /tmp/packwiz list"
