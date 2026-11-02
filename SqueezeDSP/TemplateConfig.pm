package Plugins::SqueezeDSP::TemplateConfig;
=pod
 Moved config settings to own file so as to be easier to manage

=cut

sub get_config_revision
{
	my $configrevision = "0.3.14";
	return $configrevision;
}

# 0.3.14 A DSF goes to our engine's own DSD decoder at every DSD target (Configuration::dsdEngine): the 48k family
#        (Upsample's fixed 96k, 384k, 768k) and 705.6k now too, through the decoder's stage 3. A DFF keeps its decoder.
# 0.3.13 A DSF goes to our engine's own DSD decoder (_dsdEngine) wherever the player's DSD target is in the 44.1k family
#        and at most 352.8k: no ffmpeg, dsdplay or sox, the low-pass and gain from the player's settings. A DFF, and a
#        higher or 48k-family target, keep their decoder as in 0.3.12.
# 0.3.12 DSD on a server with no ffmpeg goes through DSDPlayer's dsdplay wherever that plugin is enabled (dxr=dsdplay):
#        it seeks in the file at once, where sox decodes up to the point; LMS's sox where it is not, as in 0.3.11.
# 0.3.11 DSD on a server with no ffmpeg (LMS bundles none): LMS's own sox decodes DSF and DFF and its rate takes them to
#        the target, so a DSP-on player still plays DSD through the DSP (dxr=sox); with ffmpeg the rule is as before.
#        Reduced streams (templates MP3, OPS, OGG): MP3 at a player's Bitrate Limiting, and an app asking for a lossy
#        format by its list (LyrPlay's Opus, Ogg and MP3 modes) gets it through the DSP at the track's own rate, never
#        upsampled.
# 0.3.10 Upsample's ffmpeg reads a WAV or AIFF stream (the I rule) as pipe:0, as every other Upsample stage reads its
#        pipe: LMS hands an I rule's $FILE$ over as '-', which ffmpeg opens only with the fd protocol (6.1 and up).
#        A PCM-mode player's FLAC rules (taken under Bitrate Limiting) stop at FLAC's 384k cap, DSD in them too:
#        they had the WAV targets, 705.6k / 768k, which LMS's flac 1.3.4 refuses. Upsample off is unchanged.
# 0.3.09 DSD reaches its target through ffmpeg's soxr when that ffmpeg has it (the player's filter options while
#        Upsample is on, precision 28 otherwise), instead of ffmpeg's own resampler; without soxr the rule is as
#        before. The marker records which (dxr=), so a new ffmpeg rewrites the rules.
# 0.3.08 Seeking a local file: MP3 and AAC go back to the stdin rule only (LMS feeds them from their audio, past any
#        cover art, and seeks them by bytes, as the release did); Ogg and Opus seek with sox's trim, Ogg FLAC with
#        flac's --skip, WAV and AIFF with the engine's own --skip (our engine build, engine/), ffmpeg's -ss when
#        Upsample reads the file. PCM mode's WAV states its length through the engine's --wavlength (WavLength.pm).
# 0.3.07 PCM mode: the engine writes the WAV and is the last stage; sox follows it only to take a track down to the
#        player's max (the D rule). WavLength.pm knows our WAV rules from LMS's own transcoder, so the --no-glob
#        marker is gone, and its filter now takes the place of the sox that only rewrote the engine's header.
# 0.3.06 PCM mode's sox carries --no-glob, the marker WavLength.pm looks for to add its filter: the WAV header then
#        states the length still to play (plus 1 s) instead of "unknown" beside a RIFF size of 72.
# 0.3.05 Upsample phase: intermediate and minimum go through sox's rate (ffmpeg's soxr is linear only), with 6 dB
#        taken off before sox (it clips inside rate) and put back in float after it, so every phase plays at the
#        same level and an over up to +6 dBTP still reaches the engine. Linear and Upsample off are unchanged.
# 0.3.04 Upsample's ffmpeg reads with -probesize 32. By default it holds back its output until it has read about 5 s
#        of input, and LMS feeds a stream (and a resume, which reopens it) only as output comes back, so a Qobuz
#        track or a resume took seconds to start. The output is byte for byte the same.
# 0.3.03 Upsample (per player, default off): every type but DSD is resampled before the engine by ffmpeg's soxr in
#        floating point, to the player's top rate in the track's own family, and reaches the engine as 32-bit float,
#        so inter-sample peaks arrive as real samples the engine can meter (sox's rate clips them). One rule per type
#        with D: the same route takes a track above the player's max down. Without Upsample the rules are unchanged.
# 0.3.02 Every type that can read a file has an F-only rule first: an I rule fed local files on stdin, which LMS feeds
#        64 KB per 0.4 s while nothing comes out, so a track with embedded cover art took 6 to 20 s to start. Raw PCM
#        into the engine from every decoder that writes to a pipe (AAC, MP3, Ogg, Opus, WMA, HLS, DSD): a WAV header
#        with an unknown length makes the engine print a warning into its audio output, and sox or flac then reject it.
# 0.3.01 FLAC and Ogg FLAC go back to one raw rule for files and streams (IFT and IF, as upstream and LMS's own rules). LMS
#        resumes a paused or seeked remote FLAC with frames only, no STREAMINFO, and flac's WAV header for that says 0 channels,
#        0 Hz and 0 bits, so the engine got nothing and LMS skipped to the next track.
# 0.3.00 (fork, docs/fork-plan.md steps 3-5) Rules are generated from one decoder table. Every decoder hands the engine a WAV header
#        (local FLAC and Ogg FLAC keep the raw path), the engine writes 24-bit WAV, and LMS's flac encodes it, or sox when LMS asks for a
#        lower rate. WAV template for players that read a WAV header; FLACENGINE (the engine encodes, as 0.1.71) as a diagnostic.
#        DSD decoded straight to the player's DXD target. sls and wvpx added, wma takes stdin. The MP3 and WAV16 templates are gone.
# 0.2.02 Added soc values for SpotOn plugin and amended dsf and dff to use pcm_s24le for 24 bit output, added wav16 output template for non-FLAC clients
# 0.2.01 Amended all settings to use new SqueezeDSP adapter Eliminated wav16 replace with MP3
# 0.1.11 corrected ogf line which had duplicate convolver app.
# 0.1.10 added hls flc and wav
# 1.0.9 alc format is sensitive to where the parameters are for faad, standardised for all faad based transcoding
# 1.0.8 Change to mp3 as raw format caused chipmunk chatter on talkSport, issue with wmadec decoding addressed
# 1.0.7 Correction to mp4 and mp4x, alc, alcx formats
# 1.0.6 Correction to ogf format
# 1.0.5 major tidy up. SqueezeDSP can handle raw pcm and aif properly now so no need for intermediate Sox process. fixed some typos and formatting errors too, mainly on 16 bit.
# 1.0.4 one off beta fix
# 1.03 fix for pausing Qobuz
# transcode for 24-bit FLAC output (sb2, sb3, transporter)
# aac & mp4 added by jb
# deleted aap entry - jb
# replaced alc - jb
# added spt (spotty) (jb)
# changed FLAC compressions level to 0 (was 5)
# v.05 amended flac to FRIT - needed change to DSP to handle error when track skipping
# amended Ogg-flc, wasn't playing, but now working. amended aac to make it more standard, no diff noted however
# amended aif file, now playing (bit of a long path); added mp4x and alcx
# v.07 added in mapping for ogf and corrected spt wav mapiing for 16 bit
# v.08 aif not being used in preference to default, change command header
# v.09 amended settings for Spotty, use URL not FILE fixed bit rate and removed reliance on SoX for PCM to Wav conversion
# v.10 amended settings for mp3, use sox instead of lame as problem with skipping within a track.
# v.11 amended settings for aif, use sox instead of flac for 24 bit, wasn't working and brings it into line with 16 bit.

# The engine and the arguments every rule gives it; each template adds the output format.
my $ENGINE = '[$CONVAPP$] --trackURL=$URL$ --Clientid="$CLIENTID$" --bitsout=24';

# Raw PCM into the engine, described on its command line. A decoder writing WAV into a pipe cannot know the length,
# and the engine's WAV reader then prints "unknown size from header" to its standard output, which is the audio
# (foxWavReader.go:1006): sox or flac after it rejects the stream, so it never starts (ledger THE ENGINE PRINTS INTO
# THE AUDIO). So every decoder whose length is not in its header hands the engine raw PCM, as upstream's rules for
# FLAC, AAC, HLS and WMA did. $RAW takes LMS's view of the track (for FLAC, the STREAMINFO its scanner read); $RAW16
# and $RAW24 fix the depth the decoder writes. ffmpeg and sox are also told the rate and channels to write ($FFRAW,
# $SOXRAW), so they write exactly what the engine is told, whatever LMS's view of the source is. FLAC stays raw for
# the reason it always was: a resumed or seeked remote FLAC has no STREAMINFO (ledger A RESUMED FLAC STREAM HAS NO
# STREAMINFO). Spotty (always 16/44.1/2) and SpotOn are raw too.
my $RAW    = '--formatin=PCM --be=false --bitsin=$SAMPLESIZE$ --samplerate=$SAMPLERATE$ --channels=$CHANNELS$';
my $RAW16  = '--formatin=PCM --be=false --bitsin=16 --samplerate=$SAMPLERATE$ --channels=$CHANNELS$';
my $RAW24  = '--formatin=PCM --be=false --bitsin=24 --samplerate=$SAMPLERATE$ --channels=$CHANNELS$';
my $FFRAW  = '-c:a pcm_s24le -ar $SAMPLERATE$ -ac $CHANNELS$ -f s24le -';
my $SOXRAW = '-t raw -e signed-integer -b 24 -L -r $SAMPLERATE$ -c $CHANNELS$ -';
my $SPTRAW = '--formatin=PCM --be=false --bitsin=16 --samplerate=44100 --channels=2';

# Upsample (Upsample.pm): ffmpeg's soxr resamples in floating point before the engine, and the
# engine reads 32-bit raw as IEEE float (ledger ENGINE RAW 32-BIT IS FLOAT), so nothing between the
# decoder and the engine can clip an inter-sample peak (ledger SOX CLIPS INSIDE RATE).
#   $UPRATE$  the target, written per player as SQDSPUP_<44.1k>_<48k> and picked per track at stream start
#   $UPOPTS$  the player's soxr options, written per player
# ffmpeg reads a pipe as pipe:0, which every version opens ('-' needs the fd protocol since 6.1,
# fftools ffmpeg_demux.c). LMS hands an I rule's $FILE$ over as '-', so a stdin rule names pipe:0 itself.
my $UPAF  = '-af aresample=$UPRATE$:$UPOPTS$ -c:a pcm_f32le -ac $CHANNELS$ -f f32le -';
my $F32IN = '--formatin=PCM --be=false --bitsin=32 --samplerate=$UPRATE$ --channels=$CHANNELS$';

# The intermediate and minimum phases (Upsample.pm @PHASES): sox's rate, which clips inside rate, after
# HEADROOM dB taken off (by sox's gain, or in float by a decoding ffmpeg, whose overs sox's input would
# clip), writing float; then ffmpeg puts the HEADROOM back in float, so the engine meets the same level
# and the same overs as with soxr. -D: no dither on sox's float output.
#   $UPSOX$  the player's sox rate options (Upsample::soxOptions), written per player
my $HEAD    = 6;
my $SOXF32  = '-t raw -e floating-point -b 32 -L -';
my $SOXRATE = 'rate $UPSOX$ $UPRATE$';
my $UPBACK  = "[ffmpeg] -loglevel quiet -probesize 32 -f f32le -ar \$UPRATE\$ -ac \$CHANNELS\$ -i pipe:0 -af volume=${HEAD}dB -c:a pcm_f32le -ar \$UPRATE\$ -ac \$CHANNELS\$ -f f32le -";

# How each input type reaches the engine: [ type, capabilities (no D), command up to the engine, kind ].
#
# A WAV header reaches the engine only where its length is real: faad on an MP4 container, mac, wvunpack and sls
# reading a file, and the engine reading a WAV or AIFF itself (--input $FILE$).
#
# Stream modes (Song.pm:402-409): a local file is offered I then F, a stream I then R, a transcoder seek F or R
# alone; a rule takes the first offered mode it lists. A rule listing both I and F gets local files on stdin, which
# LMS feeds 32 KB at a time and, while nothing comes out (a decoder reading past embedded cover art), only every
# 0.4 s (Pipeline.pm:253-337, Web/HTTP.pm RETRY_TIME): 6 to 20 s before a track starts, so no gapless (ledger A
# LOCAL FILE ON STDIN CRAWLS). So a type LMS feeds from byte 0 (FLAC, Ogg, Opus, WAV, AIFF: art included) has an
# F-only rule first, which a file takes; a stream is rejected by it and takes the next rule. Types whose decoder
# needs a seekable file (faad on MP4, mac, wvunpack, sls) stay F only, as LMS's own rules for them are.
#
# Seeking (ledger SEEKING: THE RELEASE'S WAY WHERE IT FITS). LMS seeks a rule with T by restarting it with $START$
# (Song.pm:402-433), and an I rule alone by bytes. MP3 and ADTS AAC have only the I rule: LMS feeds a local file
# from its audio, past the tag and its art (MP3.pm:132, Movie.pm:107), and seeks it by bytes, as the release did
# (ledger MP3 AND AAC SKIP THEIR TAGS ON STDIN). Every F rule that can start part way in says T, so a seek stays on
# our rule and LMS's own T rules for wav, aif and ops never take it (ledger A SEEK DROPS THE DSP): Ogg and Opus with
# sox's trim after its output, Ogg FLAC with flac's --skip, WAV and AIFF with the engine's own --skip (our build,
# engine/). A stream rule shares the command; LMS removes a $START$ its rule does not define.
#
# kind 'dxd': ffmpeg decodes DSD straight to the player's DXD target ($DXD$, filled in per player when the conf
# is written), which is never above its max, so the rule is single and declares D bare (LMS always asks for it:
# it records a DSF's rate as the DSD rate). $DXDAF$ is the resampler that takes it there, filled in with the
# conf: ffmpeg's soxr when that ffmpeg has it (Upsample::dsdFilter), or nothing, and then ffmpeg's own (-ar).
# With no ffmpeg on the server, DSDPlayer's dsdplay does both instead where that plugin is enabled (_dsdPlay), else
# LMS's own sox (_dsdSox).
my $FLAC = '[flac] -dcs --force-raw-format --endian=little --sign=signed $START$ $END$ -- $FILE$ | ' . $ENGINE . ' ' . $RAW;
my $OGF  = '[flac] --ogg -dcs --force-raw-format --endian=little --sign=signed $START$ $END$ -- $FILE$ | ' . $ENGINE . ' ' . $RAW;
my $AAC  = '[faad] -q -w -f 2 $FILE$ | ' . $ENGINE . ' ' . $RAW16;
my $MP3  = '[lame] --mp3input --decode --silent -t $FILE$ - | ' . $ENGINE . ' ' . $RAW16;
my $OGG  = '[sox] -q -t ogg $FILE$ ' . $SOXRAW . ' $START$ | ' . $ENGINE . ' ' . $RAW24;
my $OPUS = '[sox] -q -t opus $FILE$ ' . $SOXRAW . ' $START$ | ' . $ENGINE . ' ' . $RAW24;
# the engine reading a WAV or AIFF itself, from $START$ on a seek (--skip=m:ss.ff, LMS's %t)
my $SELF = $ENGINE . ' --input $FILE$ $START$';
my $WMA  = '[ffmpeg] -loglevel quiet $START$ -i $FILE$ $END$ ' . $FFRAW . ' | ' . $ENGINE . ' ' . $RAW24;
my $DSD  = '[ffmpeg] -loglevel quiet $START$ -i $FILE$ $END$ $DXDAF$ -c:a pcm_s24le -ar $DXD$ -ac $CHANNELS$ -f s24le - | '
         . $ENGINE . ' --formatin=PCM --be=false --bitsin=24 --samplerate=$DXD$ --channels=$CHANNELS$';
my @INPUTS = (
	[ 'aac',  'I',                                      $AAC ],
	[ 'aif',  'FT:{START=--skip=%t}',                   $SELF ],
	[ 'aif',  'I',                                      $SELF ],
	[ 'alc',  'FT:{START=-j %s}U:{END=-e %u}',          '[faad] -q -w -f 1 $START$ $END$ $FILE$ | ' . $ENGINE ],
	[ 'alcx', 'FT:{START=-j %s}U:{END=-e %u}',          '[faad] -q -w -f 1 $START$ $END$ $FILE$ | ' . $ENGINE ],
	[ 'ape',  'F',                                      '[mac] $FILE$ - -d | ' . $ENGINE ],
	[ 'dsf',  'FT:{START=-ss %s}U:{END=-to %u}',        $DSD, 'dxd' ],
	[ 'dff',  'FT:{START=-ss %s}U:{END=-to %u}',        $DSD, 'dxd' ],
	[ 'flc',  'FT:{START=--skip=%t}U:{END=--until=%v}', $FLAC ],
	[ 'flc',  'IT:{START=--skip=%t}U:{END=--until=%v}', $FLAC ],
	[ 'hls',  'RB:{BITRATE=-B %B}T:{START=-ss %s}',     '[ffmpeg] -loglevel quiet $START$ -i $FILE$ ' . $FFRAW . ' | ' . $ENGINE . ' ' . $RAW24 ],
	[ 'mov',  'FR',                                     '[mov123] $FILE$ | ' . $ENGINE ],
	[ 'mp3',  'I',                                      $MP3 ],
	[ 'mp4',  'FT:{START=-j %s}U:{END=-e %u}',          '[faad] -q -w -f 1 $START$ $END$ $FILE$ | ' . $ENGINE ],
	[ 'mp4x', 'FT:{START=-j %s}U:{END=-e %u}',          '[faad] -q -w -f 1 $START$ $END$ $FILE$ | ' . $ENGINE ],
	[ 'mpc',  'IR',                                     '[mppdec] --raw-le --silent --prev --gain 3 - - | ' . $ENGINE . ' ' . $RAW16 ],
	[ 'ogf',  'FT:{START=--skip=%t}U:{END=--until=%v}', $OGF ],
	[ 'ogf',  'I',                                      $OGF ],
	[ 'ogg',  'FT:{START=trim %s}',                     $OGG ],
	[ 'ogg',  'IR',                                     $OGG ],
	[ 'ops',  'FB:{BITRATE=--abr %B}T:{START=trim %s}', $OPUS ],
	[ 'ops',  'IB:{BITRATE=--abr %B}',                  $OPUS ],
	[ 'sls',  'F',                                      '[sls] $FILE$ - -s | ' . $ENGINE ],
	[ 'soc',  'I',                                      $ENGINE . ' ' . $RAW ],
	[ 'spt',  'RT:{START=--start-position %s}',         '[spotty] -n Squeezebox -c "$CACHE$" --single-track $URL$ --bitrate 320 --disable-discovery --disable-audio-cache $START$ | '
	                                                    . $ENGINE . ' ' . $SPTRAW ],
	[ 'wav',  'FT:{START=--skip=%t}',                   $SELF ],
	[ 'wav',  'I',                                      $SELF ],
	[ 'wma',  'FT:{START=-ss %t}U:{END=-to %v}',        $WMA ],
	[ 'wma',  'IT:{START=-ss %t}U:{END=-to %v}',        $WMA ],
	[ 'wmal', 'FT:{START=-ss %t}U:{END=-to %v}',        $WMA ],
	[ 'wmal', 'IT:{START=-ss %t}U:{END=-to %v}',        $WMA ],
	[ 'wmap', 'FT:{START=-ss %t}U:{END=-to %v}',        $WMA ],
	[ 'wmap', 'IT:{START=-ss %t}U:{END=-to %v}',        $WMA ],
	[ 'wvp',  'FT:{START=--skip=%t}U:{END=--until=%v}', '[wvunpack] $FILE$ -wq $START$ $END$ -o - | ' . $ENGINE ],
	[ 'wvpx', 'FT:{START=--skip=%t}U:{END=--until=%v}', '[wvunpack] $FILE$ -wq $START$ $END$ -o - | ' . $ENGINE ],
);

# One rule as LMS reads it: header, capabilities line, command (TranscodingHelper.pm:79-130).
sub _rule {
	my ( $in, $out, $caps, $command ) = @_;
	return "$in $out \$MODEL\$ \$CLIENTID\$\n\t# $caps\n\t$command\n\n";
}

# DSD on a server with no ffmpeg (LMS bundles none: ledger LMS BUNDLES NO FFMPEG): LMS's own sox reads DSF and DFF
# (its dsf and dff readers, in every platform's build) and its rate takes the audio to the same target, written as
# float. sox clips inside rate only above full scale, and DSD's full level sits 6 dB under PCM's; on the bench its
# level matched ffmpeg's below 20 kHz (ledger DSD FALLS BACK TO SOX, UPSAMPLE STILL NEEDS SOXR). A seek trims before
# rate, from %s; a cue track's end is its length (%w, end minus start), cut by a second trim, which counts from the
# first one's output. The gain 0 before a seek's trim changes no sample: it keeps trim from being the first effect,
# which sox turns into a seek in the file, and LMS's sox seeks a DSF or DFF to twice the time asked (bench
# 2026-11-02: trim 1 starts at 2 s, trim 2 of a 4 s file plays nothing; ledger SOX SEEKS DSD TO TWICE THE TIME).
# So a seek decodes up to its point at the DSD rate instead, before the costly rate. [ capabilities without D,
# command ] for one input type.
sub _dsdSox {
	my $in = shift;
	return [ 'FT:{START=gain 0 trim %s}U:{END=trim 0 %w}',
		"[sox] -q -t $in \$FILE\$ -t raw -e floating-point -b 32 -L -r \$DXD\$ -c \$CHANNELS\$ - \$START\$ \$END\$ rate -v -L \$DXD\$ | "
		. $ENGINE . ' --formatin=PCM --be=false --bitsin=32 --samplerate=$DXD$ --channels=$CHANNELS$' ];
}

# DSD on a server with no ffmpeg where DSDPlayer is enabled: its dsdplay (LMS finds it in that plugin's Bin folder,
# added for every enabled plugin before any starts, PluginManager.pm:337-362) decodes with dsd2pcm, the code ffmpeg's
# DSD decoder comes from, and takes the audio to the target with soxr (-R vL: very high quality, linear phase, as
# sox's rate -v -L). It writes 24-bit FLAC, which LMS's flac decodes for the engine. It seeks in the file itself, at
# once: a DFF to the byte, a DSF to the 4096-byte block at or before the time (up to 11.6 ms early on DSD64). -s and
# -e take m:ss.cc (%t, %v), the end counted from the file's start. Its FLAC holds no rate above 655350 (DSDPlayer's
# own build refused 705.6k on plex: ledger A PCM PLAYER'S FLAC RULES STOP AT 384K); $DXD$ is 352.8k at most here, as
# Upsample, which raises it, needs ffmpeg. Bench 2026-11-02: below 20 kHz the level of sox and ffmpeg, DSD64 and
# DSD128, every seek and cue track in place (ledger DSD ON DSDPLAY). [ capabilities without D, command ]
sub _dsdPlay {
	return [ 'FT:{START=-s %t}U:{END=-e %v}',
		'[dsdplay] -R vL $START$ $END$ -r $DXD$ $FILE$ | [flac] -dcs --force-raw-format --endian=little --sign=signed - | '
		. $ENGINE . ' --formatin=PCM --be=false --bitsin=24 --samplerate=$DXD$ --channels=$CHANNELS$' ];
}

# A DSF on our engine's own DSD decoder (engine/foxAudioLib/foxAudioDecoder/foxDSDReader; ledger OUR OWN DSD DECODER):
# the engine reads the file itself and decodes it straight to the player's $DXD$, with the low-pass and gain from the
# player's settings (DSDCutoff, DSDGain, read by the engine at each track). It seeks to the byte (--skip, %t) and
# ends a cue track (--until, %v), both from the file's start, each exactly the full decode's samples. Every target
# this plugin gives, 44.1k to 768k in either family (Configuration::dsdEngine; the 48k family and 705.6k from DSD64
# through the decoder's stage 3). A DFF keeps its decoder: a DST-compressed one is ffmpeg's alone.
# [ capabilities without D, command ]
sub _dsdEngine {
	return [ 'FT:{START=--skip=%t}U:{END=--until=%v}',
		$ENGINE . ' --formatin=DSD --input $FILE$ --samplerate=$DXD$ $START$ $END$' ];
}

# One input type's capabilities (without D) and command; a DSD type's decoder is ffmpeg, or with the option dsd
# 'dsdplay' DSDPlayer's dsdplay, or with 'sox' LMS's sox; a DSF with dsdEngine our engine. $o: the conf's options.
sub _input {
	my ( $input, $o ) = @_;
	my ( $in, $caps, $command, $kind ) = @$input;
	my $dsd = ( $o || {} )->{dsd};
	if ( ( $kind // '' ) eq 'dxd' ) {
		return @{ _dsdEngine() }  if $in eq 'dsf' && ( $o || {} )->{dsdEngine};
		return @{ _dsdPlay() }    if ( $dsd // '' ) eq 'dsdplay';
		return @{ _dsdSox($in) } if ( $dsd // '' ) eq 'sox';
	}
	return ( $caps, $command );
}

# The upsample route for one input command: the same decoder, then ffmpeg's soxr to $UPRATE$ in
# float, then the engine told 32-bit float at $UPRATE$. One route per way a decoder can hand over:
# ffmpeg decoding (it resamples in the same pass), raw PCM (ffmpeg reads it as the engine was told),
# a WAV header with the file's length (ffmpeg reads the header), and the engine reading a WAV or AIFF
# itself (ffmpeg reads the file instead). Any other shape dies, so a new input type needs a route.
# -probesize 32: every input here is PCM whose format is given or in a header, so ffmpeg has nothing
# to analyse; its default reads about 5 s of input before the first output, and LMS feeds a stream
# 32 KB at a time only as output comes back, every 0.4 s while none does (ledger
# A LOCAL FILE ON STDIN CRAWLS). The ffmpeg decoder route keeps ffmpeg's defaults: it decodes.
my $FFUP = '[ffmpeg] -loglevel quiet -probesize 32';
sub _ffup { return "$FFUP $_[0] $UPAF" }

# The sox route for one input: sox reads it as given ($input: its format options and file), takes the
# headroom off unless a decoding ffmpeg already has ($gain), resamples, writes float, and ffmpeg puts the
# headroom back. Integers enter sox exactly; float (which can hold overs) reaches it only from an ffmpeg
# that has already taken the headroom off, since sox's input conversion clips at full scale too.
sub _soxup {
	my ( $input, $gain ) = @_;
	return "[sox] -q -D $input $SOXF32 " . ( $gain ? "gain -$HEAD " : '' ) . "$SOXRATE | $UPBACK";
}
my $FFHEAD = "-af volume=-${HEAD}dB -c:a pcm_f32le -ar \$SAMPLERATE\$ -ac \$CHANNELS\$ -f f32le -";
my $SOXFIN = '-t raw -e floating-point -b 32 -L -r $SAMPLERATE$ -c $CHANNELS$ -';

# $sox: the intermediate or minimum phase (sox's rate instead of ffmpeg's soxr)
# $stdin: the rule reads LMS's pipe (I), where $FILE$ would reach ffmpeg as '-'
sub _upsample {
	my ( $cmd, $sox, $stdin ) = @_;
	my $tail;
	# ffmpeg reads the file instead of the engine, so a seek's $START$ is ffmpeg's (_build gives it -ss)
	if ( $cmd eq $SELF ) {
		my $file = $stdin ? 'pipe:0' : '$FILE$';
		return _ffup("\$START\$ -i $file") . " | $ENGINE $F32IN" unless $sox;
		# a WAV or AIFF can be float: ffmpeg reads it and takes the headroom off in float
		return "$FFUP \$START\$ -i $file $FFHEAD | " . _soxup( $SOXFIN, 0 ) . " | $ENGINE $F32IN";
	}

	$tail = "$FFRAW | $ENGINE $RAW24";
	if ( index( $cmd, '[ffmpeg] ' ) == 0 && substr( $cmd, -length($tail) ) eq $tail ) {
		return substr( $cmd, 0, -length($tail) ) . "$UPAF | $ENGINE $F32IN" unless $sox;
		return substr( $cmd, 0, -length($tail) ) . "$FFHEAD | " . _soxup( $SOXFIN, 0 ) . " | $ENGINE $F32IN";
	}

	for my $raw ( [ $RAW, 's$SAMPLESIZE$le', '$SAMPLESIZE$' ], [ $RAW16, 's16le', 16 ], [ $RAW24, 's24le', 24 ] ) {
		$tail = "$ENGINE $raw->[0]";
		next unless substr( $cmd, -length($tail) ) eq $tail;
		return substr( $cmd, 0, -length($tail) ) . _ffup("-f $raw->[1] -ar \$SAMPLERATE\$ -ac \$CHANNELS\$ -i pipe:0") . " | $ENGINE $F32IN" unless $sox;
		return substr( $cmd, 0, -length($tail) ) . _soxup( "-t raw -e signed-integer -b $raw->[2] -L -r \$SAMPLERATE\$ -c \$CHANNELS\$ -", 1 ) . " | $ENGINE $F32IN";
	}

	# Spotty writes 16/44.1/2 whatever LMS's view of the track is
	$tail = "$ENGINE $SPTRAW";
	if ( substr( $cmd, -length($tail) ) eq $tail ) {
		( my $af = $UPAF )  =~ s/\$CHANNELS\$/2/;
		( my $in = $F32IN ) =~ s/\$CHANNELS\$/2/;
		return substr( $cmd, 0, -length($tail) ) . "$FFUP -f s16le -ar 44100 -ac 2 -i pipe:0 $af | $ENGINE $in" unless $sox;
		( my $up = _soxup( '-t raw -e signed-integer -b 16 -L -r 44100 -c 2 -', 1 ) ) =~ s/\$CHANNELS\$/2/g;
		return substr( $cmd, 0, -length($tail) ) . "$up | $ENGINE $in";
	}

	# a decoder's WAV header with the file's length: integer PCM from faad, mac, wvunpack, sls, mov123
	$tail = "| $ENGINE";
	if ( substr( $cmd, -length($tail) ) eq $tail ) {
		return substr( $cmd, 0, -length($tail) ) . '| ' . _ffup('-i pipe:0') . " | $ENGINE $F32IN" unless $sox;
		return substr( $cmd, 0, -length($tail) ) . '| ' . _soxup( '--ignore-length -t wav -', 1 ) . " | $ENGINE $F32IN";
	}
	die "TemplateConfig: no upsample route for: $cmd\n";
}

# Every input type, written with each template's output stage. $nod and $d return [caps, command] for the
# rule without D and the rule with it; a repeated header is the next instance, tried only when the one
# before is rejected, so a track LMS must take down to the player's max lands on the D rule. $plain is the
# output stage alone. With $up every type but DSD takes the upsample route, in one rule with D: within the
# max or above it, the track reaches the engine at the target, so nothing after the engine resamples.
# $up: 0 none, 1 ffmpeg's soxr (linear phase), 2 sox's rate (intermediate or minimum phase).
# $o: the conf's options (Configuration::template): dsd, with no ffmpeg on the server 'dsdplay' (_dsdPlay) or 'sox'
# (_dsdSox), else ffmpeg decodes DSD; dsdEngine, a DSF on our engine (_dsdEngine).
sub _build {
	my ( $out, $nod, $d, $plain, $up, $o ) = @_;
	my $text = "\n";
	for my $input (@INPUTS) {
		my ( $in, undef, undef, $kind ) = @$input;
		my ( $caps, $command ) = _input( $input, $o );
		if ( ( $kind // '' ) eq 'dxd' ) {
			$text .= _rule( $in, $out, "${caps}D", $plain->($command) );
			next;
		}
		if ( $up ) {
			# a WAV or AIFF file: ffmpeg reads it on this route, and starts it with -ss (as LMS's own rules do)
			my $c = $caps;
			$c =~ s/\{START=--skip=%t\}/{START=-ss %t}/ if $command eq $SELF;
			$text .= _rule( $in, $out, "${c}D", $plain->( _upsample( $command, $up == 2, $caps !~ /F/ ) ) );
			next;
		}
		$text .= _rule( $in, $out, @{ $nod->( $caps, $command ) } ) if $nod;
		$text .= _rule( $in, $out, @{ $d->( $caps, $command ) } );
	}
	return $text;
}

# Each template takes $up (_build's) and the conf's options, a hash: dsd ('dsdplay' or 'sox' with no ffmpeg on the
# server), dsdEngine (a DSF on our engine) and, for MP3 at a Bitrate Limiting, kbps.
#
# FLAC (the default): the engine writes 24-bit WAV and LMS's own flac encodes it, the topology that ran without
# static before 20 June. A lower rate goes through sox's resampler instead; --ignore-length because the
# engine's WAV size is input size x bit ratio when its input had a size (LyrionDSPProcessAudio.go:651-658).
sub template_FLAC24
{
	my ( $up, $o ) = @_;
	my $flac = '[flac] -cs -0 --totally-silent --ignore-chunk-sizes -';
	return _build( 'flc',
		sub { [ $_[0], "$_[1] --formatout=WAV | $flac" ] },
		sub { [ "$_[0]D:{RESAMPLE=rate -v %d}", "$_[1] --formatout=WAV | [sox] -q --ignore-length -t wav - -t flac -C 0 -b 24 - \$RESAMPLE\$" ] },
		sub { "$_[0] --formatout=WAV | $flac" },
		$up, $o || {},
	);
}

# Diagnostic, round 1 only (OutputPath "engine" in the player's settings, no UI): the same inputs, but the
# engine encodes FLAC itself through its sox adapter and resamples itself, as 0.1.71 did, so a flip changes
# the encoder and nothing else (docs/fork-plan.md step 4, the static A/B).
sub template_FLACENGINE
{
	my ( $up, $o ) = @_;
	return _build( 'flc',
		undef,
		sub { [ "$_[0]D:{RESAMPLE=--maxSampleRate=%d}", "$_[1] \$RESAMPLE\$ --formatout=FLC" ] },
		sub { "$_[0] --formatout=FLC" },
		$up, $o || {},
	);
}

# A reduced stream (Configuration::reducedOutput): MP3 at the player's Bitrate Limiting, or the lossy format an app
# asks for by its list, at Mobile Transcode Rules' qualities (Opus 256 kbps, Ogg Vorbis quality 10, MP3 320 kbps).
# Simon 2026-11-01: "we would never upsample to then downgrade to bit rate limiting but DSP may still be required".
# So the engine runs at the track's own rate (no Upsample route, nothing resamples before it), and its 24-bit WAV
# goes straight into the encoder, which reads its unknown length as "to the end" (lame's and opusenc's --ignorelength,
# sox's --ignore-length) and lowers the rate itself where its format needs it: lame to 48k or under, opusenc always
# to 48k, so their D is bare (a --resample to the player's max, 192 for a 192k player, is a rate lame refuses).
# Vorbis keeps the track's rate, so on D sox's rate takes it to the player's max, as the WAV rules do. DSD reaches
# the engine at 44.1k (Configuration::dxdTarget) and declares D bare here too. One rule per input type, within the
# max or above it. B on every one: LMS asks for it under Bitrate Limiting, and with no rule having it the last
# candidate wins, another plugin's rule or LMS's own (ledger THE LAST CANDIDATE WINS A WANT). The MP3 rule honours
# it; an Opus or Ogg one runs only where the server has no lame to honour it with.
#   [ the stream format, the capabilities every rule adds, the encoder from the options ]
my %REDUCED = (
	MP3 => [ 'mp3', 'BD', sub {
		# LMS's own rules' way under a limit (convert.conf flc mp3: -q $QUALITY$, --abr), Mobile Transcode's for an app
		'[lame] --silent --ignorelength ' . ( $_[0]{kbps} ? "-q \$QUALITY\$ --abr $_[0]{kbps}" : '-q 0 -b 320' ) . ' - -' } ],
	OPS => [ 'ops', 'BD', sub { '[opusenc] --quiet --ignorelength --bitrate 256 - -' } ],
	OGG => [ 'ogg', 'BD:{RESAMPLE=rate -v %d}', sub { '[sox] -q --ignore-length -t wav - -t ogg -C 10 - $RESAMPLE$' } ],
);

sub _reduced {
	my ( $fmt, $o ) = @_;
	$o ||= {};
	my ( $out, $add, $encoder ) = @{ $REDUCED{$fmt} };
	my $enc = $encoder->($o);
	my $text = "\n";
	for my $input (@INPUTS) {
		my ( $caps, $command ) = _input( $input, $o );
		if ( ( $input->[3] // '' ) eq 'dxd' ) {
			# DSD at 44.1k already: nothing to take down, whatever LMS asks
			( my $bare = $add ) =~ s/:\{[^}]*\}//g;
			( my $e = $enc ) =~ s/ \$RESAMPLE\$//;
			$text .= _rule( $input->[0], $out, "$caps$bare", "$command --formatout=WAV | $e" );
			next;
		}
		$text .= _rule( $input->[0], $out, "$caps$add", "$command --formatout=WAV | $enc" );
	}
	return $text;
}

sub template_MP3 { return _reduced( 'MP3', $_[1] ) }
sub template_OPS { return _reduced( 'OPS', $_[1] ) }
sub template_OGG { return _reduced( 'OGG', $_[1] ) }

# PCM mode: 24-bit WAV with its header, for a player that reads one (it lists wav: a squeezelite started with
# -W). The header carries the true depth, rate, channels and byte order, so what LMS announces (the source
# track's format, Squeezebox.pm:595-608) does not matter. The engine writes it and is the last stage; a track
# above the player's max goes through sox's rate after it (the D rule, --ignore-length because the engine's
# size is its input's scaled, LyrionDSPProcessAudio.go:651-658). Into a pipe neither states the true length,
# so at stream start WavLength.pm adds a filter after the last stage that states the length still to play
# plus a second: squeezelite stops at a stated size or the stream's end, whichever is first (pcm.c:207).
sub template_WAV
{
	my ( $up, $o ) = @_;
	my $sox = '[sox] -q --ignore-length -t wav - -t wav -b 24 -';
	return _build( 'wav',
		sub { [ $_[0], "$_[1] --formatout=WAV" ] },
		sub { [ "$_[0]D:{RESAMPLE=rate -v %d}", "$_[1] --formatout=WAV | $sox \$RESAMPLE\$" ] },
		sub { "$_[0] --formatout=WAV" },
		$up, $o || {},
	);
}

1
;
