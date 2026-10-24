package Plugins::SqueezeDSP::TemplateConfig;
=pod
 Moved config settings to own file so as to be easier to manage

=cut

sub get_config_revision
{
	my $configrevision = "0.3.00";
	return $configrevision;
}

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

# Raw PCM described by LMS's view of the track. Used only where that view is reliable or fixed.
my $RAW = '--formatin=PCM --be=false --bitsin=$SAMPLESIZE$ --samplerate=$SAMPLERATE$ --channels=$CHANNELS$';

# How each input type reaches the engine: [ type, capabilities (no D), command up to the engine, kind ].
#
# Every decoder writes WAV, so the engine reads the true depth, rate and channels from the header instead of
# LMS's view of the track (TranscodingHelper.pm:448-450 fills the tokens with 16 bits and 44.1k when it does not
# know). The raw path stays only for a local FLAC or Ogg FLAC file (LMS's view comes from the file's own
# STREAMINFO, and flac refuses a WAV header past ~4 GB of PCM), Spotty (always 16/44.1/2) and SpotOn.
#
# Stream modes (Song.pm:402-409): a local file is offered I then F, a stream I then R, a transcoder seek F or R
# alone; a rule takes the first offered mode it lists. So a type split in two puts the F-only rule first: a
# file takes it, a stream is rejected by it and takes the next. Types whose decoder needs a seekable file
# (faad on MP4, mac, wvunpack, sls) stay F only, as LMS's own rules for them are.
#
# kind 'dxd': ffmpeg decodes DSD straight to the player's DXD target ($DXD$, filled in per player when the conf
# is written), which is never above its max, so the rule is single and declares D bare (LMS always asks for it:
# it records a DSF's rate as the DSD rate).
my @INPUTS = (
	[ 'aac',  'IF',                                     '[faad] -q -w -f 1 $FILE$ | ' . $ENGINE ],
	[ 'aif',  'IF',                                     $ENGINE . ' --input $FILE$' ],
	[ 'alc',  'FT:{START=-j %s}U:{END=-e %u}',          '[faad] -q -w -f 1 $START$ $END$ $FILE$ | ' . $ENGINE ],
	[ 'alcx', 'FT:{START=-j %s}U:{END=-e %u}',          '[faad] -q -w -f 1 $START$ $END$ $FILE$ | ' . $ENGINE ],
	[ 'ape',  'F',                                      '[mac] $FILE$ - -d | ' . $ENGINE ],
	[ 'dsf',  'FT:{START=-ss %s}U:{END=-to %u}',        '[ffmpeg] -loglevel quiet $START$ -i $FILE$ $END$ -c:a pcm_s24le -ar $DXD$ -f wav - | ' . $ENGINE, 'dxd' ],
	[ 'dff',  'FT:{START=-ss %s}U:{END=-to %u}',        '[ffmpeg] -loglevel quiet $START$ -i $FILE$ $END$ -c:a pcm_s24le -ar $DXD$ -f wav - | ' . $ENGINE, 'dxd' ],
	[ 'flc',  'FT:{START=--skip=%t}U:{END=--until=%v}', '[flac] -dcs --force-raw-format --endian=little --sign=signed $START$ $END$ -- $FILE$ | ' . $ENGINE . ' ' . $RAW ],
	[ 'flc',  'I',                                      '[flac] -dcs -- $FILE$ | ' . $ENGINE ],
	[ 'hls',  'RB:{BITRATE=-B %B}T:{START=-ss %s}',     '[ffmpeg] -loglevel quiet $START$ -i $FILE$ -c:a pcm_s24le -f wav - | ' . $ENGINE ],
	[ 'mov',  'FR',                                     '[mov123] $FILE$ | ' . $ENGINE ],
	[ 'mp3',  'IF',                                     '[lame] --mp3input --decode --silent $FILE$ - | ' . $ENGINE ],
	[ 'mp4',  'FT:{START=-j %s}U:{END=-e %u}',          '[faad] -q -w -f 1 $START$ $END$ $FILE$ | ' . $ENGINE ],
	[ 'mp4x', 'FT:{START=-j %s}U:{END=-e %u}',          '[faad] -q -w -f 1 $START$ $END$ $FILE$ | ' . $ENGINE ],
	[ 'mpc',  'IR',                                     '[mppdec] --silent --prev --gain 3 - - | ' . $ENGINE ],
	[ 'ogf',  'F',                                      '[flac] --ogg -dcs --force-raw-format --endian=little --sign=signed -- $FILE$ | ' . $ENGINE . ' ' . $RAW ],
	[ 'ogf',  'I',                                      '[flac] --ogg -dcs -- $FILE$ | ' . $ENGINE ],
	[ 'ogg',  'IFR',                                    '[sox] -t ogg $FILE$ -t wav - | ' . $ENGINE ],
	[ 'ops',  'IFB:{BITRATE=--abr %B}',                 '[sox] -q -t opus $FILE$ -t wav - | ' . $ENGINE ],
	[ 'sls',  'F',                                      '[sls] $FILE$ - -s | ' . $ENGINE ],
	[ 'soc',  'I',                                      $ENGINE . ' ' . $RAW ],
	[ 'spt',  'RT:{START=--start-position %s}',         '[spotty] -n Squeezebox -c "$CACHE$" --single-track $URL$ --bitrate 320 --disable-discovery --disable-audio-cache $START$ | '
	                                                    . $ENGINE . ' --formatin=PCM --be=false --bitsin=16 --samplerate=44100 --channels=2' ],
	[ 'wav',  'IF',                                     $ENGINE . ' --input $FILE$' ],
	[ 'wma',  'IFT:{START=-ss %t}U:{END=-to %v}',       '[ffmpeg] -loglevel quiet $START$ -i $FILE$ $END$ -c:a pcm_s24le -f wav - | ' . $ENGINE ],
	[ 'wmal', 'IFT:{START=-ss %t}U:{END=-to %v}',       '[ffmpeg] -loglevel quiet $START$ -i $FILE$ $END$ -c:a pcm_s24le -f wav - | ' . $ENGINE ],
	[ 'wmap', 'IFT:{START=-ss %t}U:{END=-to %v}',       '[ffmpeg] -loglevel quiet $START$ -i $FILE$ $END$ -c:a pcm_s24le -f wav - | ' . $ENGINE ],
	[ 'wvp',  'FT:{START=--skip=%t}U:{END=--until=%v}', '[wvunpack] $FILE$ -wq $START$ $END$ -o - | ' . $ENGINE ],
	[ 'wvpx', 'FT:{START=--skip=%t}U:{END=--until=%v}', '[wvunpack] $FILE$ -wq $START$ $END$ -o - | ' . $ENGINE ],
);

# One rule as LMS reads it: header, capabilities line, command (TranscodingHelper.pm:79-130).
sub _rule {
	my ( $in, $out, $caps, $command ) = @_;
	return "$in $out \$MODEL\$ \$CLIENTID\$\n\t# $caps\n\t$command\n\n";
}

# Every input type, written with each template's output stage. $nod and $d return [caps, command] for the
# rule without D and the rule with it; a repeated header is the next instance, tried only when the one
# before is rejected, so a track LMS must take down to the player's max lands on the D rule.
sub _build {
	my ( $out, $nod, $d, $dxd ) = @_;
	my $text = "\n";
	for my $input (@INPUTS) {
		my ( $in, $caps, $command, $kind ) = @$input;
		if ( ( $kind // '' ) eq 'dxd' ) {
			$text .= _rule( $in, $out, "${caps}D", $dxd->($command) );
			next;
		}
		$text .= _rule( $in, $out, @{ $nod->( $caps, $command ) } ) if $nod;
		$text .= _rule( $in, $out, @{ $d->( $caps, $command ) } );
	}
	return $text;
}

# FLAC (the default): the engine writes 24-bit WAV and LMS's own flac encodes it, the topology that ran without
# static before 20 June. A lower rate goes through sox's resampler instead; --ignore-length because the
# engine's WAV size is input size x bit ratio when its input had a size (LyrionDSPProcessAudio.go:651-658).
sub template_FLAC24
{
	my $flac = '[flac] -cs -0 --totally-silent --ignore-chunk-sizes -';
	return _build( 'flc',
		sub { [ $_[0], "$_[1] --formatout=WAV | $flac" ] },
		sub { [ "$_[0]D:{RESAMPLE=rate -v %d}", "$_[1] --formatout=WAV | [sox] -q --ignore-length -t wav - -t flac -C 0 -b 24 - \$RESAMPLE\$" ] },
		sub { "$_[0] --formatout=WAV | $flac" },
	);
}

# Diagnostic, round 1 only (OutputPath "engine" in the player's settings, no UI): the same inputs, but the
# engine encodes FLAC itself through its sox adapter and resamples itself, as 0.1.71 did, so a flip changes
# the encoder and nothing else (docs/fork-plan.md step 4, the static A/B).
sub template_FLACENGINE
{
	return _build( 'flc',
		undef,
		sub { [ "$_[0]D:{RESAMPLE=--maxSampleRate=%d}", "$_[1] \$RESAMPLE\$ --formatout=FLC" ] },
		sub { "$_[0] --formatout=FLC" },
	);
}

# PCM mode: 24-bit WAV with its header, for a player that reads one (it lists wav: a squeezelite started with
# -W). The header carries the true depth, rate, channels and byte order, so what LMS announces (the source
# track's format, Squeezebox.pm:595-608) does not matter. Every rule ends in sox, which rewrites the header with
# an unknown size: squeezelite stops at a real one (pcm.c:117-125), and the engine's can be wrong.
sub template_WAV
{
	my $sox = '[sox] -q --ignore-length -t wav - -t wav -b 24 -';
	return _build( 'wav',
		undef,
		sub { [ "$_[0]D:{RESAMPLE=rate -v %d}", "$_[1] --formatout=WAV | $sox \$RESAMPLE\$" ] },
		sub { "$_[0] --formatout=WAV | $sox" },
	);
}

1
;
