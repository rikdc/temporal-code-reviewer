#!/bin/bash
# Add dependencies between Lyon LLM Integration tasks
# Format: bd dep add <issue-that-depends> <issue-it-depends-on>

set -e

echo "Adding dependencies between Lyon tasks..."
echo ""

# Foundation dependencies
echo "=== Foundation Dependencies ==="
bd dep add LYON-kxh LYON-i08  # Diff Cache depends on Config
bd dep add LYON-0xh LYON-i08  # LLM Client depends on Config
bd dep add LYON-qr5 LYON-kxh  # Diff Fetcher depends on Diff Cache

# Agent dependencies (all agents need LLM Client, Types, and Prompts)
echo ""
echo "=== Agent Dependencies ==="
bd dep add LYON-hlv LYON-0xh  # Security Agent depends on LLM Client
bd dep add LYON-hlv LYON-meu  # Security Agent depends on Types
bd dep add LYON-hlv LYON-4jf  # Security Agent depends on Prompts

bd dep add LYON-6xk LYON-0xh  # Style Agent depends on LLM Client
bd dep add LYON-6xk LYON-meu  # Style Agent depends on Types
bd dep add LYON-6xk LYON-4jf  # Style Agent depends on Prompts

bd dep add LYON-x60 LYON-0xh  # Logic Agent depends on LLM Client
bd dep add LYON-x60 LYON-meu  # Logic Agent depends on Types
bd dep add LYON-x60 LYON-4jf  # Logic Agent depends on Prompts

bd dep add LYON-jyc LYON-0xh  # Docs Agent depends on LLM Client
bd dep add LYON-jyc LYON-meu  # Docs Agent depends on Types
bd dep add LYON-jyc LYON-4jf  # Docs Agent depends on Prompts

# Integration dependencies
echo ""
echo "=== Integration Dependencies ==="
bd dep add LYON-bzg LYON-qr5  # Workflow Update depends on Diff Fetcher
bd dep add LYON-bzg LYON-hlv  # Workflow Update depends on Security Agent
bd dep add LYON-bzg LYON-6xk  # Workflow Update depends on Style Agent
bd dep add LYON-bzg LYON-x60  # Workflow Update depends on Logic Agent
bd dep add LYON-bzg LYON-jyc  # Workflow Update depends on Docs Agent

bd dep add LYON-o2x LYON-i08  # Main Init depends on Config
bd dep add LYON-o2x LYON-0xh  # Main Init depends on LLM Client
bd dep add LYON-o2x LYON-qr5  # Main Init depends on Diff Fetcher
bd dep add LYON-o2x LYON-hlv  # Main Init depends on Security Agent
bd dep add LYON-o2x LYON-6xk  # Main Init depends on Style Agent
bd dep add LYON-o2x LYON-x60  # Main Init depends on Logic Agent
bd dep add LYON-o2x LYON-jyc  # Main Init depends on Docs Agent

# Unit test dependencies
echo ""
echo "=== Unit Test Dependencies ==="
bd dep add LYON-3hs LYON-i08  # Config Tests depend on Config
bd dep add LYON-48z LYON-kxh  # Cache Tests depend on Diff Cache
bd dep add LYON-7mx LYON-0xh  # LLM Tests depend on LLM Client

# Integration test dependencies
echo ""
echo "=== Integration Test Dependencies ==="
bd dep add LYON-m04 LYON-qr5  # Diff Fetcher Tests depend on Diff Fetcher
bd dep add LYON-66l LYON-hlv  # Security Agent Tests depend on Security Agent
bd dep add LYON-dsz LYON-6xk  # Style Agent Tests depend on Style Agent
bd dep add LYON-4x3 LYON-x60  # Logic Agent Tests depend on Logic Agent
bd dep add LYON-nfn LYON-jyc  # Docs Agent Tests depend on Docs Agent
bd dep add LYON-qxm LYON-bzg  # Workflow Tests depend on Workflow Update

# Documentation dependencies
echo ""
echo "=== Documentation Dependencies ==="
bd dep add LYON-7yw LYON-i08  # Setup Docs depend on Config
bd dep add LYON-dh2 LYON-7yw  # README Update depends on Setup Docs
bd dep add LYON-5a6 LYON-o2x  # Testing Checklist depends on Main Init

# E2E and quality dependencies
echo ""
echo "=== E2E and Quality Dependencies ==="
bd dep add LYON-zkh LYON-o2x  # E2E Validation depends on Main Init
bd dep add LYON-zkh LYON-66l  # E2E Validation depends on Security Agent Tests
bd dep add LYON-zkh LYON-dsz  # E2E Validation depends on Style Agent Tests
bd dep add LYON-zkh LYON-4x3  # E2E Validation depends on Logic Agent Tests
bd dep add LYON-zkh LYON-nfn  # E2E Validation depends on Docs Agent Tests
bd dep add LYON-zkh LYON-aa2  # E2E Validation depends on Demo Script

bd dep add LYON-qbj LYON-o2x  # Linting depends on Main Init
bd dep add LYON-rtn LYON-o2x  # Makefile depends on Main Init
bd dep add LYON-rtn LYON-qbj  # Makefile depends on Linting

# UI and logging dependencies
echo ""
echo "=== UI and Observability Dependencies ==="
bd dep add LYON-9kz LYON-bzg  # Dashboard UI depends on Workflow Update
bd dep add LYON-323 LYON-bzg  # Workflow Logging depends on Workflow Update
bd dep add LYON-m0n LYON-bzg  # Architecture Docs depend on Workflow Update

echo ""
echo "✅ All dependencies added successfully!"
echo ""
echo "Use 'bd ready' to see tasks ready to start (no blockers)"
echo "Use 'bd blocked' to see tasks waiting on dependencies"
