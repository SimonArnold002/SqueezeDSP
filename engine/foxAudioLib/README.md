## foxAudioLib

`foxAudioLib` is a modular library of **Go packages for audio DSP** written to support the SqueezeDSP plugin and to build the FoxLyrion DSP executable. The project provides reusable components for decoding, encoding, convolution, FFT processing, filtering, resampling, and utility routines.

### Features

- **Audio I/O**
  - Flexible decoder and encoder with plugin support
  - Buffered stdin reader for interactive/pipe workflows

- **Signal Processing**
  - Direct and partitioned convolution (low-latency, efficient)
  - FFT implementations (internal and RustFFT wrapper)

- **DSP Utilities**
  - Parametric EQ builder
  - Normalization and peak/FFT gain tools
  - Optional high-quality resampling via SoX
  - Test signal generator
  - Integrated logging

### Design

The library emphasizes simplicity, portability, and explicit control. Packages can be used independently or together to build complete DSP pipelines. Primarily intended for audio processing tasks where Go’s portability and concurrency model are advantageous.

### Example Pipeline

For examples look at FoxLyrionDSP and in particular the process-audio package as this is where the pipeline is initialised and run.
