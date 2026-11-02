package Plugins::SqueezeDSP::Upsample;
# Upsampling (docs/fork-plan.md step 7), per player, default off.
#
# With Upsample on, a track is resampled BEFORE the engine by ffmpeg's soxr in floating point, to
# the highest rate of its own family the player takes, and reaches the engine as 32-bit float
# (the engine's raw path reads --bitsin=32 as IEEE float: ledger ENGINE RAW 32-BIT IS FLOAT).
# sox cannot do this: its effects pass 32-bit integers, so `rate` clips every inter-sample peak
# above full scale (ledger SOX CLIPS INSIDE RATE). In float the overs reach the engine as real
# samples, its input and output peaks report them, and the player's Preamp gives the headroom
# (ledger UPSAMPLE PLACEMENT).
#
# The rules carry the target as SQDSPUP_<44.1k family>_<48k family>, written per player from its
# max. A conf rule cannot pick the family per track (its only rate selector is D, and a conf line
# does no arithmetic), so tokenizeConvertCommand2 is wrapped to fill it in at stream start: the
# plugin's only hook into LMS core. The same wrap states PCM mode's WAV length (WavLength.pm).
#
# The target (@TARGETS): 'family', each track to the top rate of its own family (the default), or
# 'fixed', every track to the player's top rate, so the player never changes rate between tracks
# (Simon 2026-10-25: "upsample to a fixed single rate max rate being that of what the dac reports back
# to LMS"). A fixed target is written as both families' (SQDSPUP_384000_384000), so the pick at stream
# start needs nothing new; DSD follows it too (Configuration.pm dxdTarget), through soxr (dsdFilter).
use strict;
use Slim::Utils::Misc;
use Slim::Player::TranscodingHelper;

# Rates above 384k are not offered with FLAC out until the rig's FLAC encoders are measured above 655,350 Hz.
use constant CAP => 384000;
# WAV out (PCM mode) has no encoder in the way, and its player reads the rate from the header, past LMS's own
# rate table, which stops at 384k (Squeezebox.pm pcm_sample_rates). 768k is the top rate any player here takes
# (our squeezelite on the Eversolo DMP-A8); nothing above it has been tried (Simon 2026-11-01: "the everolo
# supports upto 768 PCM").
use constant CAP_WAV => 768000;
use constant PLACEHOLDER => 'SQDSPUP_';

# The max rates the page offers below the player's own top rate, 0 being the player's own (Simon 2026-11-01:
# "an option in SDP to set a max rate for the fixed rate and not just go with the highest it says. We need options
# to get round any performance issues"). A max caps both families: 384k gives 352.8k / 384k, or 384k fixed.
my @MAXRATES = ( 768000, 384000, 192000, 96000 );

# the soxr precisions the page offers; above 20 bits soxr computes in 64-bit (soxr.c:439)
my @PRECISIONS = ( 20, 24, 28, 33 );
use constant DEFAULT_PRECISION => 28;

# The passband (Simon 2026-11-02): per family 99% unless the player chose another; a fixed target 95% unless it chose
# lower, and never above 95% (a higher one stored plays at 95%). Auto (0, soxr's own edge: flat to about 92% of the
# track's Nyquist, -3 dB at 95%, measured) stays a choice wherever it is stored.
use constant PASSBAND_FAMILY => 0.99;
use constant PASSBAND_FIXED  => 0.95;

# The filter phase. ffmpeg hands soxr only a quality number (libswresample/soxr_resample.c:48), so its
# soxr is linear phase only. Intermediate and minimum go through sox's rate effect instead (rate.c is
# libsoxr's parent: the same precision, passband and roll-off options, plus -I/-M), which passes 32-bit
# integers and so clips an inter-sample peak inside rate (ledger SOX CLIPS INSIDE RATE): those rules take
# 6 dB off before sox and put it back in float after it (TemplateConfig.pm $HEAD), so every phase plays at
# the same level and an over up to +6 dBTP still reaches the engine's meters (ledger UPSAMPLE PHASE).
my @PHASES = qw(linear intermediate minimum);

my @TARGETS = qw(family fixed);

our $INSTALLED = 0;
my $orig;

# ffmpeg as LMS's [ffmpeg] finds it (findbin: LMS's own Bin folders first, then the PATH),
# and whether that binary has soxr. undef until checked, then cached for the server's life:
# a newly installed ffmpeg needs a restart, as LMS's own binary cache does.
our $FFMPEG;
our $SOXR;
# sox as LMS's [sox] finds it, for the intermediate and minimum phases; cached the same way
our $SOX;

# The highest 44.1k-family and 48k-family rates at or below the player's max and $cap (cap(): CAP
# unless given), or () when the max is unknown, so no upsample rule is written for that player. A
# player whose max is below 48k sends 48k-family tracks to its top 44.1k-family rate.
sub targets {
    my ( $max, $cap ) = @_;
    return () unless defined $max && $max =~ /^\d+$/ && $max >= 44100;
    $cap = CAP unless defined $cap && $cap =~ /^\d+$/ && $cap >= 44100;
    $cap = $max if $max < $cap;
    my $f44 = 44100;
    $f44 *= 2 while $f44 * 2 <= $cap;
    my $f48 = 48000;
    $f48 *= 2 while $f48 * 2 <= $cap;
    $f48 = $f44 if $f48 > $cap;
    return ( $f44, $f48 );
}

# The cap for a player's output ('WAV' or 'FLAC', Configuration::outputFormat) and the max rate chosen
# on the page (options()'s max, 0 for none): the lower of the two.
sub cap {
    my ( $out, $max ) = @_;
    my $cap = ( $out // '' ) eq 'WAV' ? CAP_WAV : CAP;
    return ( $max && $max < $cap ) ? $max : $cap;
}

# The fixed target: the player's top rate, the higher of its two targets. That is the max it reports
# whenever the max is a standard rate up to the cap (384000 above it, 768000 with WAV out, or the max
# rate chosen); a max between standard rates gets the highest standard rate below it.
sub top {
    my ( $f44, $f48 ) = @_;
    return ( $f44 // 0 ) > ( $f48 // 0 ) ? $f44 : $f48;
}

# The target for one track, from LMS's view of its rate (the number $SAMPLERATE$ carries): the
# 44.1k family when the rate divides by 11,025 (the test CapabilitiesHelper.pm:29 uses), the 48k
# family otherwise (32k and 64k included). An unknown rate takes the 44.1k family; the track
# still plays right, since every rate it reaches is resampled properly.
sub pick {
    my ( $src, $f44, $f48 ) = @_;
    return ( defined $src && $src =~ /^\d+$/ && $src && $src % 11025 ) ? $f48 : $f44;
}

sub fill {
    my ( $command, $src ) = @_;
    $command =~ s/SQDSPUP_(\d+)_(\d+)/pick( $src, $1, $2 )/ge;
    return $command;
}

# The player's filter options from its settings, checked: they end up inside a shell command,
# so only the values below ever get there. Precision: one the page offers, else 28 (soxr's very
# high). Steep: soxr's Chebyshev passband, on or off. Passband: 0 (Auto, soxr's own), or 0.80-0.995
# of Nyquist, the range ffmpeg clamps cutoff to (libswresample/soxr_resample.c); unset or anything
# else, the target's default; a fixed target at most PASSBAND_FIXED. Phase: one of @PHASES, else
# linear. Target: one of @TARGETS, else family. Max: one of @MAXRATES, else 0 (the player's own top
# rate).
sub options {
    my $prefs = shift || {};
    my $p = $prefs->{UpsamplePrecision};
    $p = DEFAULT_PRECISION unless defined $p && "$p" =~ /^\d+$/ && grep { $_ == $p } @PRECISIONS;
    my $s = ( defined $prefs->{UpsampleSteep} && "$prefs->{UpsampleSteep}" =~ /^(?:1|on|true)$/i ) ? 1 : 0;
    my $t = defined $prefs->{UpsampleTarget} ? lc "$prefs->{UpsampleTarget}" : '';
    $t = 'family' unless grep { $_ eq $t } @TARGETS;
    my $pb = $prefs->{UpsamplePassband};
    $pb = !defined $pb ? undef
        : "$pb" =~ /^0(?:\.0{1,4})?$/ ? 0
        : ( "$pb" =~ /^(?:0?\.\d{1,4}|1(?:\.0{1,4})?)$/ && $pb >= 0.8 && $pb <= 0.995 ) ? 0 + sprintf( '%.3f', $pb ) : undef;
    $pb //= PASSBAND_FAMILY;
    $pb = PASSBAND_FIXED if $t eq 'fixed' && $pb > PASSBAND_FIXED;   # so a fixed target's default is 95% too
    my $ph = defined $prefs->{UpsamplePhase} ? lc "$prefs->{UpsamplePhase}" : '';
    $ph = 'linear' unless grep { $_ eq $ph } @PHASES;
    my $m = $prefs->{UpsampleMax};
    $m = ( defined $m && "$m" =~ /^\d+$/ && grep { $_ == $m } @MAXRATES ) ? 0 + $m : 0;
    return ( precision => 0 + $p, steep => $s, passband => $pb, phase => $ph, target => $t, max => $m );
}

# ffmpeg's aresample options for soxr. internal_sample_fmt=dblp keeps swresample's buffers in
# 64-bit float, so nothing between the decoder and the engine is ever an integer that can clip.
sub ffOptions {
    my $o = shift;
    my $s = "resampler=soxr:precision=$o->{precision}:cheby=$o->{steep}";
    $s .= ":cutoff=$o->{passband}" if $o->{passband};
    return "$s:internal_sample_fmt=dblp";
}

# DSD's resampler (TemplateConfig.pm $DSD): the ffmpeg that decodes DSD takes it to its target ($rate,
# Configuration.pm dxdTarget) through soxr when this ffmpeg has soxr (Simon 2026-10-26: "we should look to
# use ffmpeg soxr then for better quality"), with the player's filter options while Upsample is on ($up,
# upsampleSpec's) and the defaults otherwise (precision 28, soxr's own passband). Linear phase on every
# phase setting: ffmpeg's soxr has no other. Without soxr, '': ffmpeg's own resampler (its -ar) takes DSD
# there, as before. The trailing space is the template's, so the rule without soxr is the old one.
sub dsdFilter {
    my ( $rate, $up ) = @_;
    return '' unless soxr();
    my %o = $up ? %$up : options( { UpsamplePassband => 0 } );   # Upsample off: soxr's own passband, as always
    return "-af aresample=$rate:" . ffOptions( \%o ) . ' ';
}

# sox's rate options for the same filter (rate.c create(), the hidden options included): -d the
# precision in bits (15-33), -I or -M the phase, -B the passband end in % (53-99.5; left out, sox
# uses soxr's own formula, 100 - 5 / TO_3dB(rej)), -f -t the no-roll-off passband and the
# high-precision clock that ffmpeg's cheby sets (SOXR_ROLLOFF_NONE | SOXR_HI_PREC_CLOCK).
sub soxOptions {
    my $o = shift;
    my @a = ( '-d', $o->{precision}, $o->{phase} eq 'minimum' ? '-M' : '-I' );
    push @a, '-B', 0 + sprintf( '%.1f', $o->{passband} * 100 ) if $o->{passband};
    push @a, '-f', '-t' if $o->{steep};
    return join( ' ', @a );
}

sub soxPath {
    return $SOX if defined $SOX;
    $SOX = eval { Slim::Utils::Misc::findbin('sox') } || '';
    return $SOX;
}

sub ffmpegPath { soxr(); return $FFMPEG }

sub soxr {
    return $SOXR if defined $SOXR;
    $FFMPEG = eval { Slim::Utils::Misc::findbin('ffmpeg') } || '';
    my $conf = $FFMPEG ? ( eval { _buildconf($FFMPEG) } // '' ) : '';
    $SOXR = ( $conf =~ /--enable-libsoxr\b/ ) ? $FFMPEG : '';
    return $SOXR;
}

sub _buildconf {
    my $bin = shift;
    return scalar `"$bin" -hide_banner -buildconf 2>&1`;
}

# Whether this player can upsample, and if not why: ('', 'hook', 'noffmpeg', 'nosoxr', 'nomax'),
# then its targets under $cap (cap(); CAP unless given).
sub available {
    my ( $client, $cap ) = @_;
    return ( 0, 'hook' ) unless $INSTALLED;
    unless ( soxr() ) {
        return ( 0, $FFMPEG ? 'nosoxr' : 'noffmpeg' );
    }
    my $max = ( defined $client && $client->can('maxSupportedSamplerate') ) ? $client->maxSupportedSamplerate() : 0;
    my @t = targets( $max, $cap );
    return ( 0, 'nomax' ) unless @t;
    return ( 1, '', @t );
}

# Wrap tokenizeConvertCommand2 (TranscodingHelper.pm:517). Both callers call it fully qualified
# (Song.pm:577, Web/HTTP.pm:2792), so the wrap reaches them. It touches only a command carrying the
# placeholder, works inside eval, and never hands ffmpeg a placeholder: on any error the 44.1k-family
# target goes in, which still plays correctly.
#
# A track already at its target rate bypasses the resampler (Simon 2026-10-25: "No need to feed material
# into it that is already the highest rate"): LMS tokenizes, in place of the Upsample rule, the rule the
# player would have with Upsample off (Configuration::plainRule), with that rule's own capabilities, since
# LMS takes START and END from its table by the rule's profile (TranscodingHelper.pm:560-569). Both are
# swapped only for the call: LMS's transcoder and its table come back as they were, so a seek or a resume
# comes through here again and is judged the same way.
sub install {
    return 1 if $INSTALLED;
    my $code = Slim::Player::TranscodingHelper->can('tokenizeConvertCommand2') or return 0;
    $orig = $code;
    no strict 'refs';
    no warnings 'redefine';
    *{'Slim::Player::TranscodingHelper::tokenizeConvertCommand2'} = \&_tokenize;
    $INSTALLED = 1;
    return 1;
}

sub _tokenize {
    my $t = $_[0];
    my $command;
    my $plain = eval { _plain($t) };
    if ($plain) {
        no warnings 'once';
        local $Slim::Player::TranscodingHelper::capabilities{ $t->{profile} } = $plain->[0];
        local $t->{command} = $plain->[1];
        $command = $orig->(@_);
    }
    else {
        $command = $orig->(@_);
    }

    if ( defined $command && index( $command, PLACEHOLDER ) >= 0 ) {
        my $src = eval { $_[0]->{sampleRate} };
        my $out = eval { fill( $command, $src ) };
        if ( defined $out && index( $out, PLACEHOLDER ) < 0 ) {
            $command = $out;
        }
        else {
            $command =~ s/SQDSPUP_(\d+)_\d+/$1/g;
        }
    }

    # PCM mode's WAV header states the length still to play (WavLength.pm); it touches only our
    # WAV rules, and on any error the command stays as it was.
    my $wav = eval { Plugins::SqueezeDSP::WavLength::apply( $command, $_[0] ) };
    return defined $wav ? $wav : $command;
}

# The track's own rate when it is already at its target, from LMS's view of it (the number $SAMPLERATE$
# carries) and the targets in the rule; else 0
sub atTarget {
    my $t = shift;
    return 0 unless ref $t eq 'HASH';
    my ( $f44, $f48 ) = ( $t->{command} // '' ) =~ /SQDSPUP_(\d+)_(\d+)/ or return 0;
    my $src = $t->{sampleRate};
    return 0 unless defined $src && $src =~ /^\d+$/ && $src > 0;
    return pick( $src, $f44, $f48 ) == $src ? 0 + $src : 0;
}

# [ capabilities, command ] of the rule that stands in for this Upsample rule when the track is already at its
# target, or undef (not one of ours, not at its target, no plain rule found)
sub _plain {
    my $t = shift;
    atTarget($t) or return undef;
    my $client = Slim::Player::Client::getClient( $t->{clientid} ) or return undef;
    my $rule = Plugins::SqueezeDSP::Configuration::plainRule( $client, $t->{profile}, $t->{streamMode} ) or return undef;
    return index( $rule->[1], PLACEHOLDER ) < 0 ? $rule : undef;
}

1;
