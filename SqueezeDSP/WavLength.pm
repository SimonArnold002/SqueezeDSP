package Plugins::SqueezeDSP::WavLength;
# PCM mode's WAV header states the length still to play (ledger THE WAV LENGTH IS STATED).
#
# Every PCM rule ends in a WAV written into a pipe: by the engine, or by sox where it takes a track
# down to the player's max (the D rule). Into a pipe neither can go back to fill its sizes in, so a
# renderer that takes a track's length from the header - the Platin Hub - showed nonsense.
#
# The length is LMS's for the track less the seek point (a seek through the rule; after a byte seek the
# whole length: seconds below), PLUS A SECOND. Squeezelite stops at a stated
# size or at the end of the stream, whichever comes first (pcm.c:207), so a size a little long ends
# where the stream does, while a short one would cut the track - and LMS's durations are rounded. With
# no length known (a radio stream) the header says "unknown", as before.
#
# At stream start the wrap of tokenizeConvertCommand2 (Upsample.pm) hands our WAV rules here, known
# from LMS's own transcoder: wav is the stream format it chose, and the rule runs our engine (its
# [SqueezeDSP] tag, by which Configuration.pm and Status.pm know our rules too).
# - The engine is the last stage (every rule but D): it is told the length (--wavlength, our engine
#   build, engine/README.md) and writes it in its own header. No other process.
# - sox follows the engine (the D rule): sox writes "unknown" beside a RIFF size of 72 whatever it is
#   told (ledger SOX CANNOT STATE A WAV LENGTH), so a filter is added as the last stage: the Perl LMS
#   runs on, rewriting only the header's size fields (RIFF, data, and a fact chunk's sample count) from
#   the rate in its own fmt. The audio passes byte for byte. No Perl (Windows LMS is one executable):
#   the header as sox wrote it.
# Not our WAV rule, or any error: the command exactly as LMS built it.
use strict;
use Config;
use File::Spec;

use constant MARGIN => 1;
# A DSF states its length before its data and has no "unknown" (foxDSDEncoder refuses to write one), so a stream LMS
# knows no length for (a radio station) is stated as a day: an upper bound like the rest, which the stream never
# reaches; the engine ends the stream with its input (foxDSDEncoder Bounded).
use constant DSF_UNKNOWN => 86400;

# The filter, run as `perl -e '<this>' <seconds>`. It contains no single quote (it goes inside
# single quotes), no [ ] (LMS's binary brackets) and no $NAME$ pair (LMS's tokens), although it is
# added after LMS has tokenised the command. It reads the header, up to the data chunk, from a
# pipe that can hand it over in any pieces; anything that is not a RIFF/WAVE header passes as it is.
my $FILTER = join ' ',
    'binmode STDIN; binmode STDOUT; my $s = shift || 0; my $h = "";',
    'sub put { my $x = shift; my $o = 0; while ($o < length $x) { my $w = syswrite(STDOUT, $x, length($x) - $o, $o); exit 0 unless $w; $o += $w } }',
    'while (length($h) < 8192) { my $n = sysread(STDIN, $h, 8192 - length($h), length($h)); last unless $n; my $d = index($h, "data"); last if $d >= 12 && length($h) >= $d + 8 }',
    'if (substr($h, 0, 4) eq "RIFF" && substr($h, 8, 4) eq "WAVE") {',
    'my ($p, $br, $al, $fa) = (12, 0, 0, 0);',
    'while ($p + 8 <= length $h) {',
    'my ($id, $len) = (substr($h, $p, 4), unpack("V", substr($h, $p + 4, 4)));',
    '($br, $al) = unpack("Vv", substr($h, $p + 16, 6)) if $id eq "fmt " && $p + 22 <= length $h;',
    '$fa = $p + 8 if $id eq "fact" && $len >= 4 && $p + 12 <= length $h;',
    'if ($id eq "data") {',
    'my ($d, $r, $f) = (0xFFFFFFFF, 0xFFFFFFFF, 0);',
    'if ($s > 0 && $br && $al) { my $n = int($s * $br / $al) * $al; ($d, $r, $f) = ($n, $p + $n, $n / $al) if $p + 8 + $n <= 0xFFFFFFFF }',
    'substr($h, $p + 4, 4) = pack("V", $d); substr($h, 4, 4) = pack("V", $r); substr($h, $fa, 4) = pack("V", $f) if $fa; last }',
    'last if $len == 0xFFFFFFFF; $p += 8 + $len + ($len % 2) } }',
    'put($h); while (1) { my $n = sysread(STDIN, my $b, 65536); last unless $n; put($b) }';

sub filter { return $FILTER }

# The Perl LMS itself runs on: on Linux and macOS a perl executable. $^X is what perl was started
# as - absolute when a script's #! line started it (LMS's launchers), but only "perl" when it was
# found on the PATH - so a relative one is replaced by the path perl was installed at. Windows LMS
# is a compiled executable with no perl beside it, so there the header stays as the engine wrote it.
sub perl {
    return undef if main::ISWINDOWS;
    my $p = $^X;
    $p = $Config{perlpath} if defined $p && !File::Spec->file_name_is_absolute($p);
    return ( defined $p && $p =~ m{perl[^/\\]*$}i && -x $p ) ? $p : undef;
}

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

# State the length in one of our WAV commands; anything else as it came. The engine's own WAV ends the
# rule when --formatout=WAV is the command's last argument (TemplateConfig.pm template_WAV); there it
# gets --wavlength. Otherwise sox writes the WAV and the filter goes after it, before LMS's own ' & |'
# suffix (added for a command LMS does not feed).
sub apply {
    my ( $command, $t ) = @_;
    return $command unless defined $command && ours($t);
    my $secs = eval { seconds($t) } || 0;
    my ( $body, $tail ) = $command =~ /^(.*?)(\s*&?\s*\|?\s*)$/s;
    if ( $t->{streamformat} eq 'dsf' ) {
        # the engine writes the DSF and ends the rule; it states its length itself when it reads a DSF file
        # (--formatin=DSD --input), and is told it when it reads a pipe (the converter to DSD: a PCM source, a DFF)
        return $command unless $body =~ /--formatout=DSF --dsdrate=\S+ --dsdmax=\d+$/ && $body !~ /--formatin=DSD --input /;
        return sprintf( '%s --wavlength=%.3f%s', $body, $secs > 0 ? $secs : DSF_UNKNOWN, $tail );
    }
    if ( $body =~ /--formatout=WAV$/ ) {
        return $command unless $secs > 0;
        return sprintf( '%s --wavlength=%.3f%s', $body, $secs, $tail );
    }
    my $perl = perl() or return $command;
    return sprintf( q{%s | "%s" -e '%s' %.3f%s}, $body, $perl, $FILTER, $secs, $tail );
}

1;
