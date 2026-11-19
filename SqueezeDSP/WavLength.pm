package Plugins::SqueezeDSP::WavLength;
# A stream's header states its length (ledger THE WAV LENGTH IS STATED, and the exact-length round,
# docs/exact-length-plan.md).
#
# Every WAV and DSF rule of ours ends with the engine writing the stream (a D rule too since the round's step 3c: the
# engine takes a track down to the player's max itself; until then sox did, after it, and a Perl filter after sox
# rewrote its header). At stream start the wrap of tokenizeConvertCommand2 (Upsample.pm) hands every command here:
# - where the frames the engine's input carries can be known (a local FLAC, MP3, APE, ALAC; a remote FLAC from its
#   start: inSamples, remoteFlacHeader below), the engine is told them (--insamples) and its header states the stream's
#   true length;
# - otherwise LMS's length for the track less the seek point (a seek through the rule; after a byte seek the whole
#   length: seconds below), PLUS A SECOND (--wavlength). Squeezelite stops at a stated size or at the end of the
#   stream, whichever comes first (pcm.c:207), so a size a little long ends where the stream does, while a short one
#   would cut the track - and LMS's durations are rounded. With no length known (a radio stream) a WAV says
#   "unknown", as before; a DSF a day (it has no "unknown").
# Our rules are known from LMS's own transcoder: wav or dsf is the stream format it chose, and the rule runs our engine
# (its [SqueezeDSP] tag, by which Configuration.pm and Status.pm know our rules too). Not ours, or any error: the
# command exactly as LMS built it.
use strict;

use constant MARGIN => 1;
# A DSF states its length before its data and has no "unknown" (gipsyDSDEncoder refuses to write one), so a stream LMS
# knows no length for (a radio station) is stated as a day: an upper bound like the rest, which the stream never
# reaches; the engine ends the stream with its input (gipsyDSDEncoder Bounded).
use constant DSF_UNKNOWN => 86400;

# The seconds to state: the track's length less where this stream starts, plus MARGIN; 0 when LMS
# does not know the length. The song being opened is already the player's streaming song when LMS
# builds its command (StreamingController.pm:1218 puts it on the queue before Song::open). Where this
# stream starts is $transcoder->{start}, which Song::open sets only for a seek through the rule (T,
# canSeek 2: Song.pm:401-432). A byte seek (canSeek 1: a remote FLAC such as Qobuz, a local file fed on
# stdin; a seek, a resume, a DSP change's re-open) has none, so the whole length is stated: too long,
# which ends where the stream does. Not the seek's time instead: a byte seek lands where its bytes
# fall, not exactly at that time, and a length too short would cut the track (ledger A BYTE SEEK
# STATES THE WHOLE LENGTH).
sub seconds {
    my $t      = shift || {};
    my $client = Slim::Player::Client::getClient( $t->{clientid} ) or return 0;
    my $c      = $client->controller or return 0;
    my $song   = $c->streamingSong or return 0;
    my $dur    = $song->duration || 0;
    my $from   = $t->{start} || 0;
    return 0 unless $dur > 0 && $dur > $from;
    return $dur - $from + MARGIN;
}

# One of our WAV rules: LMS chose wav as the stream format (getConvertCommand2 takes it from the
# rule's key), and the rule as LMS keeps it, before tokenising, runs our engine. Or one of our DSF rules
# (dsf, TemplateConfig template_DSF and template_DSFUP): apply sorts out which of those need a length.
sub ours {
    my $t = shift;
    return 0 unless ref $t eq 'HASH' && ( $t->{streamformat} // '' ) =~ /^(?:wav|dsf)$/;
    my $tag = '[' . ( $Plugins::SqueezeDSP::Plugin::convolver || 'SqueezeDSP' ) . ']';
    return index( $t->{command} // '', $tag ) >= 0 ? 1 : 0;
}

# ---- THE EXACT LENGTH ROUND, step 3 (docs/exact-length-plan.md): the frames the engine's input will carry -------------
# Where it can be known at the stream's start, from LMS's view of the track and the file itself, the engine is told it
# (--insamples=N), or told that its input's WAV header states it (--insamples=header), and its own header then states
# the stream's true length (engine length.go). Elsewhere nothing is added: the header as before.
# - FLAC, a local file: STREAMINFO's total, less where flac starts and stops for $START$ / $END$ (--skip=%t,
#   --until=%v: LMS's m:ss.ff; flac takes int(seconds x rate), 960 of 960 exact at eight rates, t/length/flac_skip.py).
#   Raw, as the rule has it: LMS's flac 1.3.4 writes NO WAV at all for over 4 GB of PCM (measured 2026-11-15).
# - MP3, a local file from its start: the LAME tag's frames x 1152 (576 below 32 kHz) less delay + 529 and padding -
#   529, as lame's decoder trims (get_audio.c:400-403; equal to its output on two of the rig's files,
#   t/length/mp3_decoders.py). No LAME tag, or a byte seek: not known.
# - APE (mac) and ALAC (faad on an MP4 from its start): the decoder's WAV header states it (measured); faad on AAC, or
#   from a start or to an end, does not (t/length/decoder_counts.py).
# - WAV, AIFF, DSF, DFF the engine reads itself: it knows; nothing added.
# - FLAC, a remote stream from its start (Qobuz; step 3b): its count is in its STREAMINFO, which only flac sees, so flac
#   writes a WAV header (its form without --force-raw-format) and the engine reads the format and the count from it
#   (--insamples=header); the same audio as the raw form, byte for byte (bench 2026-11-15). Only while LMS's length x
#   rate x channels x depth stays well under 4 GB: past it LMS's flac 1.3.4 writes nothing at all. A resume or seek
#   reaches flac as frames with no STREAMINFO (ledger A RESUMED FLAC STREAM HAS NO STREAMINFO): raw, as before.
# A byte seek (seekdata with no start through the rule) and every other remote stream: not known.

# a WAV's largest data size, less room for LMS's length being whole seconds (a Qobuz track's) and its view of the rate
use constant WAV_ROOM => 4294967295 - 64 * 1024 * 1024;

# the remote FLAC rule's raw form, as LMS tokenises it: flac's raw flags, and the engine's description of the PCM
my $FLAC_RAW   = ' --force-raw-format --endian=little --sign=signed';
my $ENGINE_RAW = qr/ --formatin=PCM --be=false --bitsin=\d+ --samplerate=\d+ --channels=\d+/;

# a remote FLAC from its start in flac's WAV-header form, or nothing
sub remoteFlacHeader {
    my ( $command, $t, $fullpath ) = @_;
    return unless _engineRule($t) && ( $t->{profile} // '' ) =~ /^flc-/ && ( $t->{streamMode} // '' ) eq 'I';
    return if !defined $fullpath || $fullpath =~ m{^file://} || $fullpath =~ /#[^-#]+-[^-#]+$/ || $t->{start};
    my $song = _song($t) or return;
    return if eval { $song->seekdata };
    my $dur  = eval { $song->duration } || 0;
    my ( $rate, $bits, $ch ) = ( $t->{sampleRate} || 0, $t->{sampleSize} || 0, $t->{channels} || 0 );
    return unless $dur > 0 && $rate > 0 && $bits > 0 && $ch > 0;
    return unless ( $dur + 1 ) * $rate * $ch * int( ( $bits + 7 ) / 8 ) < WAV_ROOM;
    my $c = $command;
    my $at = index( $c, $FLAC_RAW );
    return unless $at >= 0 && index( $c, $FLAC_RAW, $at + 1 ) < 0;
    substr( $c, $at, length $FLAC_RAW ) = '';
    $c =~ s/$ENGINE_RAW/ --insamples=header/ or return;
    return $c;
}
sub _engineRule {
    my $t = shift;
    my $tag = '[' . ( $Plugins::SqueezeDSP::Plugin::convolver || 'SqueezeDSP' ) . ']';
    return ref $t eq 'HASH' && index( $t->{command} // '', $tag ) >= 0;
}

sub _song {
    my $t      = shift || {};
    my $client = Slim::Player::Client::getClient( $t->{clientid} ) or return;
    my $c      = $client->controller or return;
    return $c->streamingSong;
}

sub _path {
    my ( $filepath, $url ) = @_;
    return $filepath if defined $filepath && $filepath ne '-' && $filepath ne '';
    return unless defined $url && $url =~ m{^file://};
    ( my $u = $url ) =~ s/#[^#]*$//;
    return Slim::Utils::Misc::pathFromFileURL($u);
}

# an ID3v2 tag at the file's start (a FLAC may carry one too): the bytes to its end
sub _id3 {
    my $fh = shift;
    seek( $fh, 0, 0 ) or return 0;
    read( $fh, my $h, 10 ) == 10 or return 0;
    return 0 unless substr( $h, 0, 3 ) eq 'ID3';
    my @b = unpack( 'C4', substr( $h, 6, 4 ) );
    return 10 + ( ( $b[0] << 21 ) | ( $b[1] << 14 ) | ( $b[2] << 7 ) | $b[3] ) + ( ( ord( substr( $h, 5, 1 ) ) & 0x10 ) ? 10 : 0 );
}

# a FLAC file's STREAMINFO: (rate, total frames), or nothing
sub flacInfo {
    my $path = shift;
    open( my $fh, '<:raw', $path ) or return;
    my $off = _id3($fh);
    seek( $fh, $off, 0 ) or return;
    read( $fh, my $b, 42 ) == 42 or return;
    return unless substr( $b, 0, 4 ) eq 'fLaC' && ( ord( substr( $b, 4, 1 ) ) & 0x7F ) == 0;
    my ( $hi, $lo ) = unpack( 'NN', substr( $b, 18, 8 ) );
    my $rate = $hi >> 12;
    return unless $rate;
    return ( $rate, ( $hi & 0xF ) * 4294967296 + $lo );
}

# where LMS's flac starts or stops for a time LMS hands it as %t / %v
sub flacAt {
    my ( $secs, $rate ) = @_;
    my ( $m, $s ) = split /:/, Slim::Utils::DateTime::fracSecToMinSec($secs);
    return int( ( $m * 60 + $s ) * $rate );
}

# an MP3 file's LAME tag: the frames lame's decoder sends from the file's start, or nothing
sub mp3Frames {
    my $path = shift;
    open( my $fh, '<:raw', $path ) or return;
    my $off = _id3($fh);
    seek( $fh, $off, 0 ) or return;
    read( $fh, my $b, 4096 ) > 200 or return;
    my $p = index( $b, "\xFF" );
    while ( $p >= 0 && $p + 4 <= length $b ) {
        my $h = unpack( 'N', substr( $b, $p, 4 ) );
        last if ( $h >> 21 ) == 0x7FF && ( ( $h >> 17 ) & 3 ) == 1;    # a frame sync, layer III
        $p = index( $b, "\xFF", $p + 1 );
    }
    return if $p < 0 || $p + 4 > length $b;
    my $h    = unpack( 'N', substr( $b, $p, 4 ) );
    my $mpeg1 = ( ( $h >> 19 ) & 3 ) == 3;
    my $mono  = ( ( $h >> 6 ) & 3 ) == 3;
    my $x     = $p + 4 + ( $mpeg1 ? ( $mono ? 17 : 32 ) : ( $mono ? 9 : 17 ) );
    return unless substr( $b, $x, 4 ) =~ /^(?:Xing|Info)$/;
    my $flags = unpack( 'N', substr( $b, $x + 4, 4 ) );
    return unless $flags & 1;
    my $frames = unpack( 'N', substr( $b, $x + 8, 4 ) );
    my $q = $x + 12 + ( ( $flags & 2 ) ? 4 : 0 ) + ( ( $flags & 4 ) ? 100 : 0 ) + ( ( $flags & 8 ) ? 4 : 0 );
    return unless $q + 24 <= length $b && substr( $b, $q, 4 ) eq 'LAME';
    my @d = unpack( 'C3', substr( $b, $q + 21, 3 ) );
    my $delay   = ( $d[0] << 4 ) | ( $d[1] >> 4 );
    my $padding = ( ( $d[1] & 0xF ) << 8 ) | $d[2];
    my $n = $frames * ( $mpeg1 ? 1152 : 576 ) - ( $delay + 529 ) - ( $padding > 529 ? $padding - 529 : 0 );
    return $n > 0 ? $n : undef;
}

# the count for this stream: frames, 'header', or nothing
sub inSamples {
    my ( $t, $filepath, $fullpath ) = @_;
    return unless _engineRule($t);
    my ($type) = ( $t->{profile} // '' ) =~ /^([^-]+)-/ or return;
    my $url = $fullpath // '';
    return unless $url =~ m{^file://};
    my ( $cueA, $cueB ) = $url =~ /#([^-#]+)-([^-#]+)$/;
    my $song = _song($t);
    my $seek = $song ? eval { $song->seekdata } : undef;
    return if $seek && !$t->{start};                     # a byte seek: where it lands is not known exactly
    my $start = ( $cueA || 0 ) + ( $t->{start} || 0 );
    my $path = _path( $filepath, $url ) or return;
    return unless -f $path;
    if ( $type eq 'flc' ) {
        my ( $rate, $total ) = flacInfo($path) or return;
        return unless $total > 0;
        my $from = $start ? flacAt( $start, $rate ) : 0;
        my $to   = $cueB  ? flacAt( $cueB, $rate )  : $total;
        $to = $total if $to > $total;
        return $to > $from ? $to - $from : undef;
    }
    return if $start || $cueB;                             # every other type: from the file's start to its end only
    return mp3Frames($path) if $type eq 'mp3';
    return 'header' if $type =~ /^(?:ape|alc|alcx)$/;
    return;
}

# ---- step 4, the exact-length contract (docs/exact-length-plan.md part B) ------------------------------------------------
# A player with our "Exact track length" option on (Gapless::exactLength: one that needs a stream's true length before
# its first byte). For it:
# - a remote FLAC whose count flac cannot state (a resume or a seek reaches it as frames only; from its start past 4 GB,
#   or with no length LMS knows: raw) has the engine read it from the stream URL (--flacseek=the byte LMS's request
#   starts at, its Range: STREAMINFO's total less the first frame's number, engine flacseek.go). Only for it: the two
#   reads wait at the stream's start, which a player that stops at the stream's end does not need;
# - FLAC out states the true total in STREAMINFO: LMS's flac without --ignore-chunk-sizes copies the engine's WAV header
#   (measured 2026-11-15, t/length/flac_encode.py), so only where that header is exact by construction: --insamples=N
#   (the plugin's own count, from the file) or a DSD file the engine reads, the WAV under 4 GB, a second or more of it.
#   Elsewhere the flag stays: an "unknown" header gives a bogus total (715,827,882 frames), a data size of 0 no stream.
sub exact {
    my $t = shift;
    return 0 unless ref $t eq 'HASH';
    return Plugins::SqueezeDSP::Gapless::exactLength( Slim::Player::Client::getClient( $t->{clientid} ) );
}

# the remote FLAC rule in its raw form, for a player that needs the count: --flacseek, or nothing
sub flacSeek {
    my ( $command, $t, $fullpath ) = @_;
    return unless _engineRule($t) && ( $t->{profile} // '' ) =~ /^flc-/ && ( $t->{streamMode} // '' ) eq 'I';
    return unless defined $fullpath && $fullpath =~ m{^https?://}i && $fullpath !~ /#[^-#]+-[^-#]+$/;
    return if $command =~ /--insamples=/ || $command !~ $ENGINE_RAW;
    return unless exact($t);
    my $song = _song($t) or return;
    my $sd = eval { $song->seekdata } || {};
    # LMS's own Range start (Slim::Player::Protocols::HTTP::requestString): a resume's restartOffset, else the seek's
    my $first = $sd->{restartOffset} || int( $sd->{sourceStreamOffset} || 0 );
    ( my $c = $command ) =~ s/(--bitsout=24)(?= |$)/$1 --flacseek=$first/ or return;
    return $c;
}

# FLAC out without --ignore-chunk-sizes where the engine's count is exact, for a player that needs it; or nothing
my $FLAC_OUT = qr/(--formatout=WAV \|[^|]* -cs -0 --totally-silent) --ignore-chunk-sizes(?= -)/;
sub flacTotal {
    my ( $command, $t, $n ) = @_;
    return unless ( $t->{streamformat} // '' ) eq 'flc' && _engineRule($t);
    return unless ( defined $n && $n =~ /^\d+$/ && $n > 0 ) || $command =~ /--formatin=DSD --input /;
    return unless exact($t);
    my $song = _song($t) or return;
    my $dur  = eval { $song->duration } || 0;
    my ($rate) = $command =~ /--upsample=(\d+)/;
    ($rate) = $command =~ /--samplerate=(\d+)/ unless $rate;
    $rate ||= $t->{sampleRate} || 0;
    my $ch = $t->{channels} || 2;
    return unless $dur >= 1 && $rate > 0 && ( $dur + 1 ) * $rate * $ch * 3 < WAV_ROOM;
    my @at = $command =~ /$FLAC_OUT/g;
    return unless @at == 1;
    ( my $c = $command ) =~ s/$FLAC_OUT/$1/;
    return $c;
}

# --insamples into one of our commands, among the engine's own arguments (after --bitsout=24, which every rule gives it)
sub withCount {
    my ( $command, $n ) = @_;
    return $command unless defined $n;
    ( my $c = $command ) =~ s/(--bitsout=24)(?= |$)/$1 --insamples=$n/ or return $command;
    return $c;
}

# State the length in one of our WAV or DSF commands; anything else as it came. The engine writes the WAV and ends
# the rule (--formatout=WAV its last argument: TemplateConfig.pm template_WAV, the D rule included); it gets
# --wavlength before LMS's own ' & |' suffix (added for a command LMS does not feed).
sub apply {
    my ( $command, $t, $filepath, $fullpath ) = @_;
    return $command unless defined $command;
    my $n = eval { inSamples( $t, $filepath, $fullpath ) };
    $command = withCount( $command, $n ) if defined $n;
    my $remote = eval { remoteFlacHeader( $command, $t, $fullpath ) };
    $command = $remote if defined $remote;
    # step 4, a player that needs the true length first
    my $seek = eval { flacSeek( $command, $t, $fullpath ) };
    $command = $seek if defined $seek;
    my $total = eval { flacTotal( $command, $t, $n ) };
    $command = $total if defined $total;
    return $command unless ours($t);
    my $secs = eval { seconds($t) } || 0;
    my ( $body, $tail ) = $command =~ /^(.*?)(\s*&?\s*\|?\s*)$/s;
    if ( $t->{streamformat} eq 'dsf' ) {
        # the engine writes the DSF and ends the rule; it states its length itself when it reads a DSF file
        # (--formatin=DSD --input), and is told it when it reads a pipe (the converter to DSD: a PCM source, a DFF)
        return $command unless $body =~ /--formatout=DSF --dsdrate=\S+ --dsdmax=\d+$/ && $body !~ /--formatin=DSD --input /;
        return sprintf( '%s --wavlength=%.3f%s', $body, $secs > 0 ? $secs : DSF_UNKNOWN, $tail );
    }
    return $command unless $body =~ /--formatout=WAV$/ && $secs > 0;
    return sprintf( '%s --wavlength=%.3f%s', $body, $secs, $tail );
}

1;
