# Flux Studio AI

## Overview

Flux Studio AI is a locally running Windows application for AI-powered image generation and image editing.

The goal of this project is to provide a straightforward desktop application that allows users to generate and modify images directly on their own hardware.

Instead of relying on hosted AI services, cloud accounts, API subscriptions or credit-based systems, Flux Studio runs its image generation locally.

## AI Model

The application uses:

- FLUX.2 Klein 4B
- Qwen3 4B text encoder
- FLUX.2 VAE
- stable-diffusion.cpp as the inference engine
- Vulkan GPU acceleration

## AMD GPU Support

Flux Studio uses Vulkan to access compatible graphics hardware.

The application has been tested with:

- AMD Radeon RX 9060 XT 16 GB
- AMD Ryzen 7 9700X

The Vulkan backend provides direct access to the AMD GPU without requiring a separate ROCm/PyTorch installation.

## Features

- Generate images from text prompts.
- Edit existing images using natural-language instructions.
- Load reference images.
- Preview input and generated images.
- Adjust subject, environment, style and lighting independently.
- Apply additional corrections to previously generated images.
- Save generated images locally.
- Run without a cloud account or API key.

## Local-First Approach

Flux Studio is designed for local image generation.

Images and prompts are processed on the user's own computer rather than being uploaded to a hosted generation service.

This also avoids dependence on the content moderation filters, account restrictions and credit systems imposed by third-party cloud image providers. Generation behavior depends on the selected local model and its capabilities.

## Technology

- C++
- Windows desktop application
- stable-diffusion.cpp
- Vulkan
- FLUX.2 Klein
- GGUF model quantization

## Project Goal

The long-term goal is to create a simple, self-contained Windows image editor that makes powerful local AI image generation accessible without requiring users to configure complex AI frameworks.

## Status

Working local image generation and image-to-image processing with AMD Vulkan GPU support.
