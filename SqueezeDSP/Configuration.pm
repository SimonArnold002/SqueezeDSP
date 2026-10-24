package Plugins::SqueezeDSP::Configuration;
use strict;
use File::Spec::Functions qw(:ALL);
use Slim::Player::TranscodingHelper;
use Plugins::SqueezeDSP::Utils;
use Plugins::SqueezeDSP::TemplateConfig;

# the state of a client whose section holds markers only: DSP off, or not connected
use constant STATE_OFF => 'dsp=off';

my %_initInProgress = ();
my $_conversionTablesClean = 0;

# ------ configuration helpers ------
sub initConfiguration {
    my $client = shift;
	#my $clientId = $client->id();
    Plugins::SqueezeDSP::Utils::debug( "client " . $client->id() ." name " . $client->name ." initializing" );

 	#return if $_initInProgress{$clientId};
    #$_initInProgress{$clientId} = 1;

	$Plugins::SqueezeDSP::Plugin::fatalError = undef;
    my $upgradeReason = '';
    my @origLines;
    my @clientIDs = sort map { $_->id() } Slim::Player::Client::clients();
    my @foundClients;
    my %markedState;
    my $marker = _markerRegex();

    @origLines = ();
    $Plugins::SqueezeDSP::Plugin::configPath = Plugins::SqueezeDSP::Utils::newConfigPath();
    Plugins::SqueezeDSP::Utils::debug ( "Config Path: " . $Plugins::SqueezeDSP::Plugin::configPath);

    if( -f $Plugins::SqueezeDSP::Plugin::configPath ) {
        open( CONFIG, "$Plugins::SqueezeDSP::Plugin::configPath" ) || do {
            Plugins::SqueezeDSP::Utils::fatal( $client, undef, "Can't read from $Plugins::SqueezeDSP::Plugin::configPath" );
            return 0;
        };
        @origLines = <CONFIG>;
        close( CONFIG );

        # Check for corruption: empty file, no begin marker, or a begin marker not in
        # today's form (upstream's read '# ##rev:...', the state field is new)
        my @beginLines = grep( /#rev:.*#client:/i, @origLines );
        my $badMarkers = grep( $_ !~ $marker, @beginLines );
        if( !@origLines || !@beginLines || $badMarkers ) {
            Plugins::SqueezeDSP::Utils::debug( "Config file missing or corrupt, will rebuild fresh" );
            $upgradeReason = "Config file missing or corrupt" unless $Plugins::SqueezeDSP::Plugin::needUpgrade;
            $Plugins::SqueezeDSP::Plugin::needUpgrade = 1;
            @foundClients = @clientIDs;   # Include all current clients
            @origLines = ();              # Discard old contents
        } else {
            my @revs = split /\./, $Plugins::SqueezeDSP::Plugin::myconfigrevision;
            for( @beginLines ) {
                if( my ( $rev, $markedClient, $state ) = $_ =~ $marker ) {
                    my @test = split /\./, $rev;
                    for my $ver ( @revs ) {
                        my $vert = shift( @test ) || 0;
                        next if $vert == $ver;
                        Plugins::SqueezeDSP::Utils::debug ("Template version higher than config: " . $ver . " - " . $vert );
                        $upgradeReason = "Previous version $rev less than my $Plugins::SqueezeDSP::Plugin::myconfigrevision ($vert less than $ver)" unless $Plugins::SqueezeDSP::Plugin::needUpgrade;
                        $Plugins::SqueezeDSP::Plugin::needUpgrade = 1;
                        last;
                    }
                    push( @foundClients, $markedClient );
                    $markedState{ lc $markedClient } = $state;
                }
            }
            unless( $Plugins::SqueezeDSP::Plugin::needUpgrade ) {
                for my $c (@clientIDs) {
                    my $ok = 0;
                    for my $cc (@foundClients) {
                        if( $c eq $cc ) {
                            $ok = 1;
                            last;
                        }
                    }
                    unless( $ok ) {
                        $upgradeReason = "Client $c was not yet registered" unless $Plugins::SqueezeDSP::Plugin::needUpgrade;
                        $Plugins::SqueezeDSP::Plugin::needUpgrade = 1;
                        Plugins::SqueezeDSP::Utils::debug ("Client $c was not yet registered" );
                        push( @foundClients, $c );
                        last;
                    }
                }
            }
            # A known client rewrites too when its state moved: DSP switched, another
            # output or model, or it was offline (markers only) when the file was written.
            unless( $Plugins::SqueezeDSP::Plugin::needUpgrade ) {
                for my $c (@clientIDs) {
                    my $state = clientState( Slim::Player::Client::getClient($c) );
                    my $was = $markedState{ lc $c };
                    next if defined $was && $was eq $state;
                    $upgradeReason = "Client $c state " . ( $was // 'none' ) . " -> $state";
                    $Plugins::SqueezeDSP::Plugin::needUpgrade = 1;
                    Plugins::SqueezeDSP::Utils::debug ( $upgradeReason );
                    last;
                }
            }
        }
    } else {
        Plugins::SqueezeDSP::Utils::debug ("Config Not found, new one created" );
        $upgradeReason = "New configuration" unless $Plugins::SqueezeDSP::Plugin::needUpgrade;
        $Plugins::SqueezeDSP::Plugin::needUpgrade = 1;
        @foundClients = @clientIDs;
    }

    upgradePrefs( @foundClients );
    return 1 unless $Plugins::SqueezeDSP::Plugin::needUpgrade;

    Plugins::SqueezeDSP::Utils::debug( "Need to rewrite " . $Plugins::SqueezeDSP::Plugin::configPath . " (" .$upgradeReason . ")" );
    open( OUT, ">$Plugins::SqueezeDSP::Plugin::configPath" ) || do {
        Plugins::SqueezeDSP::Utils::fatal( $client, undef, "Can't write to $Plugins::SqueezeDSP::Plugin::configPath" );
        return;
    };

    my $now = localtime;
    print OUT "# Modified by $Plugins::SqueezeDSP::Plugin::thisapp, $now: $upgradeReason\n";
    foreach ( @origLines ) {
        print OUT if m/# Modified by $Plugins::SqueezeDSP::Plugin::thisapp/i;
    }
    print OUT "\n";

	foreach my $clientID ( @foundClients ) {
		# A client that is not connected has no settings or model to read: markers only.
		my $client = Slim::Player::Client::getClient($clientID);
		my $state = clientState($client);

		print OUT "# #$Plugins::SqueezeDSP::Plugin::confBegin#rev:$Plugins::SqueezeDSP::Plugin::myconfigrevision#client:$clientID#state:$state# ***** BEGIN AUTOMATICALLY GENERATED SECTION - DO NOT EDIT ****\n";

		# DSP off: no rules at all, so LMS treats the player as if the plugin were absent
		if ( $state ne STATE_OFF ) {
			# 24-bit FLAC out; WAV mode adds the WAV rules ahead of it. No MP3 rules: mp3
			# is not in a DSP-on player's list (applyFormatOrder), so LMS would never reach them.
			my $flac = ( $state =~ /\bpath=engine\b/ ) ? 'FLACENGINE' : 'FLAC24';
			my @formats = ( $state =~ /\bout=WAV\b/ ) ? ( 'WAV', $flac ) : ( $flac );

			# Generate and print a template for each format
			foreach my $fmt (@formats) {
				my $t = template($clientID, $fmt, modelSlot($client));
				print OUT $t;
				print OUT "\n";
			}
		}

		print OUT "# #$Plugins::SqueezeDSP::Plugin::confEnd#client:$clientID# ***** END AUTOMATICALLY GENERATED SECTION - DO NOT EDIT *****\n";
		print OUT "\n";
	}

    close( OUT );

    # Verify that the file was written successfully and is non‑empty
    if( -s $Plugins::SqueezeDSP::Plugin::configPath == 0 ) {
        Plugins::SqueezeDSP::Utils::fatal( $client, undef, "Config file written but appears empty!" );
        return;
    }

    # Optionally set permissions so the file can be manually edited
    chmod 0666, $Plugins::SqueezeDSP::Plugin::configPath;   # or 0644 for owner‑only write

    $Plugins::SqueezeDSP::Plugin::needUpgrade = 0;
    syncCommandTable( $Plugins::SqueezeDSP::Plugin::configPath );
    return 1;
}

# The begin marker of a client's section, with the state it was written for:
#   # #SqueezeDSP#begin#rev:0.3.00#client:aa:bb:cc:dd:ee:ff#state:dsp=on,out=FLAC,...# ***** BEGIN ...
# Strict, so upstream's markers (and its greedy '#client:(.*)#') read as corrupt.
sub _markerRegex {
    my $begin = quotemeta( $Plugins::SqueezeDSP::Plugin::confBegin );
    return qr/#$begin#rev:([0-9.]+)#client:([^#\s]+)#state:([\w=,.+*-]+)#/i;
}

# The state a connected client's section must be written for, as its marker records
# it. Anything but STATE_OFF writes rules; DSP is on only when Bypass is exactly 0.
sub clientState {
    my $client = shift;
    return STATE_OFF unless defined $client;

    # No DSP below FLAC: such a player always plays as if the plugin were absent
    return STATE_OFF unless playerTakes( $client, 'dsp' );

    my $settings = Plugins::SqueezeDSP::Utils::LoadJSONFile( Plugins::SqueezeDSP::Utils::getPrefFile($client) ) || {};
    my $prefs = ref $settings->{Client} eq 'HASH' ? $settings->{Client} : {};
    return STATE_OFF unless defined $prefs->{Bypass} && $prefs->{Bypass} eq '0';

    # PCM mode is 24-bit WAV with its header, for a player that reads one; any other player
    # stays on FLAC
    my $out = ( defined $prefs->{OutputFormat} && $prefs->{OutputFormat} eq 'PCM' && playerTakes( $client, 'wav' ) ) ? 'WAV' : 'FLAC';
    my $up = ( defined $prefs->{Upsample} && $prefs->{Upsample} eq 'on' ) ? 'on' : 'off';

    my @state = ( 'dsp=on', "out=$out", "up=$up", 'model=' . modelSlot($client), 'dxd=' . dxdTarget($client) );
    # the static A/B diagnostic (no UI): the engine encodes FLAC itself, as 0.1.71 did
    push @state, 'path=engine' if defined $prefs->{OutputPath} && $prefs->{OutputPath} eq 'engine';
    return join( ',', @state );
}

# What a player can take, asked of its own list, not of the one applyFormatOrder set:
#   'dsp': 24-bit FLAC. No DSP below it (Simon, 2026-10-24): in LMS 9.1 that leaves out
#          SliMP3 and the Squeezebox 1; SoftSqueeze is left out by name, as upstream did.
#   'wav': a WAV header in a pcm stream. A squeezelite started with -W reads one and lists
#          wav (pcm.c:456-485); without it, the strm announce rules.
sub playerTakes {
    my ( $client, $what ) = @_;
    my %native = map { $_ => 1 } nativeFormats($client);
    return ( $native{flc} && ( $client->model() // '' ) ne 'softsqueeze' ) ? 1 : 0 if $what eq 'dsp';
    return $native{wav} ? 1 : 0 if $what eq 'wav';
    return 0;
}

# The player's own format list, as its HELO gave it. While DSP is on, LMS sees the list
# applyFormatOrder set, and the player's own is kept in pluginData.
sub nativeFormats {
    my $client = shift;
    return $client->formats() unless $client->can('myFormats') && ref $client->myFormats eq 'ARRAY';

    my $current = $client->myFormats;
    my $saved   = $client->pluginData('origFormats');
    my $applied = $client->pluginData('appliedFormats');
    # a list other than the one we last set came from a HELO: it is the player's own
    return ( ref $saved eq 'ARRAY' && ref $applied eq 'ARRAY' && "@$current" eq "@$applied" ) ? @$saved : @$current;
}

# Every format a SqueezeDSP rule takes as input, and pcm. With prioritizeNative on (LMS's
# default, Prefs.pm:205), getConvertCommand2 first moves the source's own format to the
# front of the player's list, wherever it sits (TranscodingHelper.pm:359-366), and pcm
# first for a wav source (:361); LMS's rule there is the player's native passthrough.
# So a DSP-on player lists none of these but its output format.
my %_covered;
sub coveredFormats {
    unless ( %_covered ) {
        for ( split /\n/, Plugins::SqueezeDSP::TemplateConfig::template_FLAC24() ) {
            $_covered{$1} = 1 if /^(\S+)\s+\S+\s+\$MODEL\$\s+\$CLIENTID\$\s*$/;
        }
        $_covered{pcm} = 1;
    }
    return sort keys %_covered;
}

# The third field of a rule header. getConvertCommand2 tries <model>-<id> before *-<id>
# (TranscodingHelper.pm:373-378), so a rule here beats another plugin's per-player rule
# (DSDPlayer writes dsf/dff -> flc as * <id>). The header splits on whitespace and loses
# anything after a '#', so an odd model falls back to '*'.
sub modelSlot {
    my $client = shift;
    my $model = defined $client ? ( $client->model() // '' ) : '';
    return $model =~ /^[A-Za-z0-9_.+-]+$/ ? $model : '*';
}

# DSD is decoded to DXD (352.8k), or to the player's top 44.1k-family rate below that.
# An unknown maximum caps nothing, as in LMS's own samplerateLimit.
sub dxdTarget {
    my $client = shift;
    my $max = ( defined $client && $client->can('maxSupportedSamplerate') ) ? ( $client->maxSupportedSamplerate() || 0 ) : 0;
    my $target = 352800;
    return $target unless $max;
    $target /= 2 while $target > $max && $target > 44100;
    return $target;
}

# Bring LMS's live transcode table in step with the conf just written. A reload cannot:
# loadConversionTables never empties the table and stores a repeated profile again as
# <profile>-N (TranscodingHelper.pm:51, :101), so a removed rule would stay live until
# restart. Instead: drop every rule that runs our engine, then add the conf's rules,
# read exactly as loadConversionTables reads a file (:79-130). LMS's native rules and
# other plugins' rules are never touched.
sub syncCommandTable {
    my $path = shift;
    my $table = \%Slim::Player::TranscodingHelper::commandTable;
    my $caps  = \%Slim::Player::TranscodingHelper::capabilities;
    my $tag   = '[' . $Plugins::SqueezeDSP::Plugin::convolver . ']';

    my $getCapabilities = Slim::Player::TranscodingHelper->can('_getCapabilities') or do {
        Plugins::SqueezeDSP::Utils::fatal( undef, undef, "LMS has no _getCapabilities: DSP changes apply after a restart" );
        return 0;
    };

    open( my $fh, '<', $path ) or do {
        Plugins::SqueezeDSP::Utils::fatal( undef, undef, "Can't read $path to update the transcode table" );
        return 0;
    };
    my @lines = <$fh>;
    close( $fh );

    my $dropped = 0;
    for my $profile ( grep { index( $table->{$_} // '', $tag ) >= 0 } keys %$table ) {
        delete $table->{$profile};
        delete $caps->{$profile};
        $dropped++;
    }

    my $added = 0;
    while ( defined( my $line = shift @lines ) ) {
        next if $line =~ /^\s*#/;
        next if $line =~ /^\s*$/;
        $line =~ s/#.*$//;
        $line =~ s/^\s*//;
        $line =~ s/\s*$//;
        next unless $line =~ /^(\S+)\s+(\S+)\s+(\S+)\s+(\S+)$/;
        my $profile = "$1-$2-$3-" . lc($4);

        # the line after a header is the capabilities line or the command, read as is
        my $next = shift @lines;
        my $capLine;
        if ( defined $next && $next =~ /^\s+#\s+(\S.*)/ ) {
            $capLine = $1;
            $next = shift @lines;
        }
        my $command = $next // '';
        $command =~ s/^\s*//;
        $command =~ s/\s*$//;
        next unless length($command) && index( $command, $tag ) >= 0;

        # a repeated profile is the next instance, tried only when the one before is rejected
        my $key = $profile;
        if ( exists $table->{$key} ) {
            my $n = 1;
            $n++ while exists $table->{"$profile-$n"};
            $key = "$profile-$n";
        }
        if ( defined $capLine ) {
            $getCapabilities->( $key, $capLine );
        } else {
            $caps->{$key} = { I => 'noArgs', F => 'noArgs' };   # LMS's default capabilities
        }
        $table->{$key} = $command;
        $added++;
    }

    Plugins::SqueezeDSP::Utils::debug( "Transcode table updated: $dropped SqueezeDSP rules dropped, $added added" );
    return 1;
}

# Every change of a player's DSP state comes through here: settings saved, a preset
# loaded, the player connected. The conf and the live table are rewritten only when a
# state moved; the format order is set every time, because a HELO resets it.
sub applyClientState {
    my $client = shift;
    return unless defined $client;
    initConfiguration($client);
    applyFormatOrder( $client, clientState($client) );
    warnMixedSync($client);
}

# Every format must reach the DSP (Simon, 2026-10-24). getConvertCommand2 walks the
# player's formats in order (TranscodingHelper.pm:371), and with prioritizeNative on it
# first moves the source's own format to the front (:359-366), so a covered format
# anywhere in the list reaches LMS's native rule ahead of ours. A DSP-on player's list is
# therefore its output format, then only formats no SqueezeDSP rule takes. DSP off puts
# the player's own list back. Players with a fixed list (no myFormats) keep it, logged.
sub applyFormatOrder {
    my ( $client, $state ) = @_;
    my $on = $state ne STATE_OFF;
    # WAV mode keeps flc after wav: a track above the max then still has our FLAC rule, and
    # a sync group of this player and one without wav still shares a format
    my @lead = ( $state =~ /\bout=WAV\b/ ) ? ( 'wav', 'flc' ) : ( 'flc' );
    my %covered = map { $_ => 1 } coveredFormats();

    unless ( $client->can('myFormats') && ref $client->myFormats eq 'ARRAY' ) {
        if ( $on ) {
            my @list = $client->formats();
            my %lead = map { $_ => 1 } @lead;
            my $pn   = Slim::Utils::Prefs::preferences('server')->get('prioritizeNative');
            # a covered format listed before ours wins; with prioritizeNative, any of them
            my ($at) = grep { $lead{ $list[$_] } } 0 .. $#list;
            my @bypass = map { $list[$_] }
                grep { $covered{ $list[$_] } && !$lead{ $list[$_] } && ( $pn || !defined $at || $_ < $at ) } 0 .. $#list;
            my $msg = $client->name . " has a fixed format list (" . join( ',', @list ) . "), left as is";
            if ( @bypass ) {
                $Plugins::SqueezeDSP::Plugin::log->warn( "$msg: " . join( ',', @bypass ) . " sources bypass the DSP"
                    . ( $pn ? " (prioritizeNative is on)" : "" ) );
            } else {
                Plugins::SqueezeDSP::Utils::debug( $msg );
            }
        }
        return;
    }

    my $current = $client->myFormats;
    my @orig = nativeFormats($client);

    my @want = @orig;
    if ( $on ) {
        my %has   = map { $_ => 1 } @orig;
        my @front = grep { $has{$_} } @lead;
        my %front = map { $_ => 1 } @front;
        @want = ( @front, grep { !$front{$_} && !$covered{$_} } @orig );
    }

    $client->pluginData( 'origFormats', [ @orig ] );
    $client->pluginData( 'appliedFormats', [ @want ] );
    return if "@want" eq "@$current";

    $client->myFormats( [ @want ] );
    Plugins::SqueezeDSP::Utils::debug( $client->name . " formats: " . join( ',', @want ) );
}

# A sync group plays one stream, built from the master's rule (TranscodingHelper.pm:320),
# so a member with another DSP state hears the master's. Logged, not changed.
sub warnMixedSync {
    my $client = shift;
    return unless defined $client && $client->isSynced(1);

    my @members = $client->syncGroupActiveMembers();
    my %on = map { $_->id() => ( clientState($_) ne STATE_OFF ? 1 : 0 ) } @members;
    my %states = map { $_ => 1 } values %on;
    return unless keys %states > 1;

    my $master = $client->master();
    $Plugins::SqueezeDSP::Plugin::log->warn( "Sync group " . join( ', ', map { $_->name } @members )
        . " mixes DSP on and off: every member hears the master's (" . $master->name . ", DSP "
        . ( $on{ $master->id() } ? 'on' : 'off' ) . ")" );
}


# Going to run this each time the server starts and update revision number for each active client
sub upgradePrefs
{
	# Going to run this each time the server starts and update revision number for each active client
	# 
	my (  @clientIDs ) = @_;
	$Plugins::SqueezeDSP::Plugin::revision;
		
	foreach my $clientID ( @clientIDs )
	{
		Plugins::SqueezeDSP::Utils::debug( "client " . $clientID );
		my $client = Slim::Player::Client::getClient( $clientID );
	# Check IMMEDIATELY after getClient()
    unless (defined($client))
		{  # Or if (not defined $client) {
        # a player named in the conf but not connected; this runs on every settings
        # save now, so it goes to the debug log rather than to STDOUT
        Plugins::SqueezeDSP::Utils::debug( "No client object for ID: " . $clientID . " (not connected)" );
        next; # Skip to the next client ID
    	}
		# Check the current and previous installed versions of the plugin,
		# in case we need to upgrade the prefs store (for this client)
		
		my $myJSONFile = Plugins::SqueezeDSP::Utils::getPrefFile( $client );
		
		unless( -f $myJSONFile )
		{
			#create default file if it does not exist
			Plugins::SqueezeDSP::Utils::debug( $client,  "Player Config File $myJSONFile not found. Creating" );
			#need to create file first
			my $myDefault = my $myDefault = {
				Client     => { Bypass => "1", OutputFormat => "FLAC" },
				ClientName => "",
				Revision   => 0
			};
			Plugins::SqueezeDSP::Utils::SaveJSONFile ($myDefault, $myJSONFile );
			#Plugins::SqueezeDSP::Utils::defaultPrefs( $client );
		}

		my $myConfig = Plugins::SqueezeDSP::Utils::LoadJSONFile ($myJSONFile);
		Plugins::SqueezeDSP::Utils::debug ( $clientID . " Config File " . $myJSONFile . " loaded" );
		my $prevrev = $myConfig->{Client}->{Version};
	
		#Now touch the version numbers	
		if( defined( $client ) )
		{
			if ( $prevrev eq $Plugins::SqueezeDSP::Plugin::revision )
			{
				Plugins::SqueezeDSP::Utils::debug ( "upgrade from " . $prevrev . " not required " )	;
			}
		
			else   
			{
				Plugins::SqueezeDSP::Utils::debug( "upgrade from " . $prevrev . " to " . $Plugins::SqueezeDSP::Plugin::revision );
				Plugins::SqueezeDSP::Utils::setPref( $client, "Version", $Plugins::SqueezeDSP::Plugin::revision );
			
			}
		}
	}
}


# find the appropriate template depending on client type
sub template {
    my ($clientID, $format, $model) = @_;
    $model = '*' unless defined $model && length $model;
    my $client = Slim::Player::Client::getClient($clientID);

    my $source = Plugins::SqueezeDSP::TemplateConfig->can( "template_$format" ) or do {
        Plugins::SqueezeDSP::Utils::fatal( $client, 'template', "no conf template $format" );
        return '';
    };
    my $template = $source->();
    
    # Substitute placeholders (same as before)
    my $pipeout;
    my $pipenul;
    if (Slim::Utils::OSDetect::OS() eq 'win') {
        $pipeout = '#PIPE#';
        $pipenul = '';
    } else {
        $pipeout = '/dev/fd/4';
        $pipenul = '4>&1 1>/dev/null';
    }
    
    my $dxd = dxdTarget($client);

    $template =~ s/\$CONVAPP\$/$Plugins::SqueezeDSP::Plugin::convolver/g;
    $template =~ s/\$MODEL\$/$model/g;
    $template =~ s/\$DXD\$/$dxd/g;
    $template =~ s/\$CLIENTID\$/$clientID/g;
    $template =~ s/\$PIPEOUT\$/$pipeout/g;
    $template =~ s/\$PIPENUL\$/$pipenul/g;
    
    return $template;
}

1;
